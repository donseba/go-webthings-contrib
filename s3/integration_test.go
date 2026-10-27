package s3_test

// These tests need an S3-compatible store. Point WT_TEST_S3 at a bucket as
//
//	WT_TEST_S3=http://ACCESS_KEY:SECRET_KEY@127.0.0.1:59000/BUCKET[?region=REGION]
//
// (https:// for TLS). The bucket is created when missing and every run works
// in its own prefix. "task s3:up", "task test:s3" and "task s3:down" run
// them against a throwaway MinIO, as by hand:
//
//	docker run -d --rm --name wt-test-minio -p 127.0.0.1:59000:9000 \
//		-e MINIO_ROOT_USER=wtwt -e MINIO_ROOT_PASSWORD=wtwtwtwt minio/minio server /data
//	WT_TEST_S3=http://wtwt:wtwtwtwt@127.0.0.1:59000/wt-test go test ./s3/
//	docker stop wt-test-minio

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"image"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	webthings "github.com/donseba/go-webthings"

	"github.com/donseba/go-webthings-contrib/s3"
)

func TestStore(t *testing.T) {
	storage := testStorage(t)
	ctx := context.Background()

	if err := storage.EnsureBucket(ctx); err != nil {
		t.Fatalf("existing bucket: %v", err)
	}

	if err := storage.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	if err := storage.Put(ctx, "hello.txt", strings.NewReader("hello"), 5); err != nil {
		t.Fatal(err)
	}

	file, err := storage.Open(ctx, "hello.txt")
	if err != nil {
		t.Fatal(err)
	}

	seeker, ok := file.(io.ReadSeeker)
	if !ok {
		t.Fatalf("%T cannot seek", file)
	}

	if _, err := seeker.Seek(1, io.SeekStart); err != nil {
		t.Fatal(err)
	}

	rest, err := io.ReadAll(seeker)
	if err != nil || string(rest) != "ello" {
		t.Fatalf("read after seek: %q %v", rest, err)
	}

	_ = file.Close()

	if err := storage.Delete(ctx, "hello.txt"); err != nil {
		t.Fatal(err)
	}

	if _, err := storage.Open(ctx, "hello.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("open deleted file: %v", err)
	}

	if err := storage.Delete(ctx, "hello.txt"); err != nil {
		t.Fatalf("delete missing file: %v", err)
	}
}

// TestApp runs the media library of a WebThings app on the store: upload,
// serve at /media/ and delete.
func TestApp(t *testing.T) {
	storage := testStorage(t)
	ctx := context.Background()

	app := webthings.New(webthings.Config{StateDir: t.TempDir(), Storage: storage})
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = app.Close() })

	var picture bytes.Buffer

	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}

	want := picture.Bytes()

	asset, err := app.Media.Upload(ctx, "pixel.png", bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}

	address := app.Media.URL(ctx, asset.ID)
	if address != "/media/"+asset.StorageKey {
		t.Fatalf("url: %s", address)
	}

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, address, nil))

	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" ||
		!bytes.Equal(response.Body.Bytes(), want) {
		t.Fatalf("GET %s: %d %s, %d bytes", address, response.Code, response.Header().Get("Content-Type"), response.Body.Len())
	}

	if err := app.Media.Delete(ctx, asset.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := storage.Open(ctx, asset.StorageKey); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("file after delete: %v", err)
	}

	response = httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, address, nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("GET deleted %s: %d", address, response.Code)
	}
}

// testStorage connects to WT_TEST_S3 with a prefix of its own.
func testStorage(t *testing.T) *s3.Storage {
	t.Helper()

	raw := os.Getenv("WT_TEST_S3")
	if raw == "" {
		t.Skip("set WT_TEST_S3=http://ACCESS_KEY:SECRET_KEY@HOST:PORT/BUCKET to test against an S3-compatible store")
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		t.Fatalf("WT_TEST_S3 is not http://ACCESS_KEY:SECRET_KEY@HOST:PORT/BUCKET: %v", err)
	}

	secret, _ := parsed.User.Password()

	storage, err := s3.New(s3.Config{
		Endpoint:  parsed.Scheme + "://" + parsed.Host,
		Region:    parsed.Query().Get("region"),
		Bucket:    strings.Trim(parsed.Path, "/"),
		AccessKey: parsed.User.Username(),
		SecretKey: secret,
		Prefix:    "wt-test-" + strings.ToLower(rand.Text()[:10]),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := storage.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}

	return storage
}
