package s3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/donseba/go-webthings/media"
)

func TestConfig(t *testing.T) {
	for _, config := range []Config{
		{Bucket: "media"},
		{Endpoint: "127.0.0.1:9000"},
		{Endpoint: "https://example.com/path", Bucket: "media"},
		{Endpoint: "127.0.0.1:9000", Bucket: "media", PublicURL: "/media"},
		{Endpoint: "127.0.0.1:9000", Bucket: "media", PublicURL: "https://cdn.example.com/?v=1"},
	} {
		if _, err := New(config); !errors.Is(err, ErrConfig) {
			t.Errorf("%+v: want ErrConfig, got %v", config, err)
		}
	}

	storage, err := New(Config{Endpoint: "https://ams3.digitaloceanspaces.com/", Bucket: "media"})
	if err != nil {
		t.Fatal(err)
	}

	if url := storage.client.EndpointURL(); url.Scheme != "https" || url.Host != "ams3.digitaloceanspaces.com" {
		t.Fatalf("endpoint: %s", url)
	}

	storage, err = New(Config{Endpoint: "http://127.0.0.1:9000", Bucket: "media", UseSSL: true})
	if err != nil || storage.client.EndpointURL().Scheme != "http" {
		t.Fatalf("an http:// endpoint is not secure: %v", err)
	}
}

func TestKeys(t *testing.T) {
	tests := []struct {
		prefix    string
		publicURL string
		object    string
		url       string
	}{
		{"", "", "abc.png", ""},
		{" /sites//example/ ", "", "sites/example/abc.png", ""},
		{"../media", "https://cdn.example.com/", "media/abc.png", "https://cdn.example.com/media/abc.png"},
		{"", "https://bucket.example.com/files", "abc.png", "https://bucket.example.com/files/abc.png"},
	}

	for _, test := range tests {
		storage, err := New(Config{Endpoint: "127.0.0.1:9000", Bucket: "media", Prefix: test.prefix, PublicURL: test.publicURL})
		if err != nil {
			t.Fatal(err)
		}

		object, err := storage.object("abc.png")
		if err != nil || object != test.object {
			t.Errorf("object with prefix %q: want %q, got %q (%v)", test.prefix, test.object, object, err)
		}

		if url := storage.URL("abc.png"); url != test.url {
			t.Errorf("URL with prefix %q and %q: want %q, got %q", test.prefix, test.publicURL, test.url, url)
		}
	}

	storage, err := New(Config{Endpoint: "127.0.0.1:9000", Bucket: "media"})
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"", "../secret", "a/b.png", ".hidden", "abc.PNG"} {
		if _, err := storage.object(key); !errors.Is(err, media.ErrUnsafeKey) {
			t.Errorf("key %q: want ErrUnsafeKey, got %v", key, err)
		}
	}
}

// TestPing checks the readiness answer against a fake store that has one
// bucket, "present".
func TestPing(t *testing.T) {
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Trim(r.URL.Path, "/") == "present" {
			w.WriteHeader(http.StatusOK)

			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(store.Close)

	ping := func(bucket string) error {
		t.Helper()

		storage, err := New(Config{Endpoint: store.URL, Region: "us-east-1", Bucket: bucket})
		if err != nil {
			t.Fatal(err)
		}

		return storage.Ping(context.Background())
	}

	if err := ping("present"); err != nil {
		t.Fatalf("existing bucket: %v", err)
	}

	if err := ping("missing"); !errors.Is(err, ErrNoBucket) {
		t.Fatalf("missing bucket: %v", err)
	}
}
