// Package s3 keeps uploaded media in an S3-compatible object store, such as
// DigitalOcean Spaces, AWS S3 or MinIO, instead of on the server's disk:
//
//	storage, err := s3.New(s3.Config{
//		Endpoint:   "https://ams3.digitaloceanspaces.com",
//		Region:     "ams3",
//		Bucket:     "example",
//		AccessKey:  os.Getenv("SPACES_KEY"),
//		SecretKey:  os.Getenv("SPACES_SECRET"),
//		Prefix:     "media",
//		PublicURL:  "https://example.ams3.cdn.digitaloceanspaces.com",
//		PublicRead: true,
//	})
//	if err := storage.EnsureBucket(ctx); err != nil { ... }
//	app := webthings.New(webthings.Config{Storage: storage, ...})
//
// With PublicURL, pages link straight to the store or its CDN, so the objects
// must be publicly readable: set PublicRead, or give the bucket a public read
// policy. Without PublicURL, WebThings serves the files with the media
// library's routes and reads them from the store, which keeps the bucket
// private.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/donseba/go-webthings/media"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrConfig reports a Config without endpoint or bucket, or with an invalid
// PublicURL.
var ErrConfig = errors.New("s3_invalid_config")

// ErrNoBucket is Ping's answer when the store can be reached but the bucket
// does not exist.
var ErrNoBucket = errors.New("s3_bucket_missing")

// keyPattern matches the keys media.Service generates: an ID with an
// extension. Anything else could reach outside the prefix.
var keyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,99}(?:\.[a-z0-9]{1,10})?$`)

// Config is where the files are kept.
type Config struct {
	// Endpoint is the store's address, for example
	// "https://ams3.digitaloceanspaces.com" or "127.0.0.1:9000". Without a
	// scheme, UseSSL decides between HTTPS and HTTP.
	Endpoint string
	// Region is the bucket's region, for example "ams3"; empty lets the
	// store tell.
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	// Prefix is a folder in the bucket, for example "sites/example", so
	// several sites can share a bucket.
	Prefix string
	// PublicURL is the public address of the bucket's root, for example a
	// CDN. Media URLs are PublicURL/Prefix/key. Empty leaves serving the
	// files to the site (media.Service.RegisterRoutes).
	PublicURL string
	UseSSL    bool
	// PublicRead uploads files with the public-read ACL, for stores that
	// support it (Spaces, S3 with ACLs enabled). MinIO ignores it and uses
	// bucket policies instead.
	PublicRead bool
}

// Storage implements media.Storage on an object store.
type Storage struct {
	client    *minio.Client
	bucket    string
	region    string
	prefix    string
	publicURL string
	public    bool
}

var (
	_ media.Storage = (*Storage)(nil)
	_ media.Pinger  = (*Storage)(nil)
)

// New connects lazily: it checks the configuration but makes no request.
// Use EnsureBucket to check the credentials and create the bucket.
func New(config Config) (*Storage, error) {
	endpoint, secure := config.Endpoint, config.UseSSL

	if rest, ok := strings.CutPrefix(endpoint, "https://"); ok {
		endpoint, secure = rest, true
	} else if rest, ok := strings.CutPrefix(endpoint, "http://"); ok {
		endpoint, secure = rest, false
	}

	endpoint = strings.TrimSuffix(endpoint, "/")
	if endpoint == "" || config.Bucket == "" || strings.Contains(endpoint, "/") {
		return nil, ErrConfig
	}

	publicURL, err := cleanPublicURL(config.PublicURL)
	if err != nil {
		return nil, err
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: secure,
		Region: config.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfig, err)
	}

	return &Storage{
		client:    client,
		bucket:    config.Bucket,
		region:    config.Region,
		prefix:    cleanPrefix(config.Prefix),
		publicURL: publicURL,
		public:    config.PublicRead,
	}, nil
}

// cleanPublicURL accepts an absolute http(s) URL without query or fragment.
func cleanPublicURL(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", ErrConfig
	}

	return strings.TrimSuffix(raw, "/"), nil
}

// cleanPrefix turns " /sites//example/ " into "sites/example/".
func cleanPrefix(prefix string) string {
	var parts []string

	for part := range strings.SplitSeq(strings.TrimSpace(prefix), "/") {
		if part != "" && part != "." && part != ".." {
			parts = append(parts, part)
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, "/") + "/"
}

// EnsureBucket creates the bucket when it does not exist, and so also checks
// the endpoint and credentials.
func (s *Storage) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}

	if exists {
		return nil
	}

	err = s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.region})

	code := minio.ToErrorResponse(err).Code
	if code == "BucketAlreadyOwnedByYou" || code == "BucketAlreadyExists" {
		return nil
	}

	return err
}

// Ping checks that the store can be reached with the credentials and that
// the bucket exists, for the app's readiness check (/readyz): a site whose
// store is unreachable is not ready, though it may still answer pages.
func (s *Storage) Ping(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}

	if !exists {
		return ErrNoBucket
	}

	return nil
}

// object is the object name of a media key.
func (s *Storage) object(key string) (string, error) {
	if !keyPattern.MatchString(key) {
		return "", media.ErrUnsafeKey
	}

	return s.prefix + key, nil
}

// Put uploads a file. Keys never change content, so browsers and CDNs may
// cache the files for a long time.
func (s *Storage) Put(ctx context.Context, key string, reader io.Reader, size int64) error {
	object, err := s.object(key)
	if err != nil {
		return err
	}

	options := minio.PutObjectOptions{
		ContentType:  mime.TypeByExtension(path.Ext(key)),
		CacheControl: "public, max-age=31536000, immutable",
	}

	if s.public {
		options.UserMetadata = map[string]string{"x-amz-acl": "public-read"}
	}

	_, err = s.client.PutObject(ctx, s.bucket, object, reader, size, options)

	return err
}

// Open returns the file, which can seek, as the media library's routes
// need. A missing file is fs.ErrNotExist.
func (s *Storage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	object, err := s.object(key)
	if err != nil {
		return nil, err
	}

	file, err := s.client.GetObject(ctx, s.bucket, object, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}

	// GetObject is lazy; Stat makes the request and reports a missing file.
	if _, err := file.Stat(); err != nil {
		_ = file.Close()

		if minio.ToErrorResponse(err).Code == minio.NoSuchKey {
			return nil, fmt.Errorf("%s: %w", key, fs.ErrNotExist)
		}

		return nil, err
	}

	return file, nil
}

// Delete removes a file; a missing file is no error.
func (s *Storage) Delete(ctx context.Context, key string) error {
	object, err := s.object(key)
	if err != nil {
		return err
	}

	return s.client.RemoveObject(ctx, s.bucket, object, minio.RemoveObjectOptions{})
}

// URL is the file's public address at PublicURL, or "" without one: the
// site serves the file then, with the media library's routes
// (media.Storage).
func (s *Storage) URL(key string) string {
	if s.publicURL == "" {
		return ""
	}

	return s.publicURL + "/" + s.prefix + key
}
