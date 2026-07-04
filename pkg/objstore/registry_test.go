package objstore

import (
	"context"
	"errors"
	"io"
	"iter"
	"net/url"
	"testing"
)

type fakeRepository struct {
	bucket string
}

func (f *fakeRepository) Upload(ctx context.Context, key string, r io.Reader) error {
	return nil
}

func (f *fakeRepository) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	return nil, ErrNotFound
}

func (f *fakeRepository) List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error] {
	return func(yield func(ObjectInfo, error) bool) {}
}

func (f *fakeRepository) Delete(ctx context.Context, key string) error {
	return ErrNotFound
}

func (f *fakeRepository) Close() error {
	return nil
}

func TestOpenUnsupportedScheme(t *testing.T) {
	_, _, err := Open(t.Context(), "azblob://bucket/prefix")
	if !errors.Is(err, ErrUnsupportedScheme) {
		t.Fatalf("expected ErrUnsupportedScheme, got %v", err)
	}
}

func TestOpenInvalidURL(t *testing.T) {
	_, _, err := Open(t.Context(), "://not a url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestOpenMissingBucket(t *testing.T) {
	Register("fake-nobucket", func(ctx context.Context, u *url.URL) (Repository, error) {
		return &fakeRepository{}, nil
	})
	_, _, err := Open(t.Context(), "fake-nobucket:///prefix")
	if err == nil {
		t.Fatal("expected error for URL without bucket")
	}
}

func TestOpenRegisteredSchemes(t *testing.T) {
	for _, scheme := range []string{"gs", "s3"} {
		if _, ok := registry[scheme]; !ok {
			t.Errorf("scheme %q not registered", scheme)
		}
	}
}

func TestOpenPrefixNormalization(t *testing.T) {
	Register("fake", func(ctx context.Context, u *url.URL) (Repository, error) {
		return &fakeRepository{bucket: u.Host}, nil
	})
	tests := []struct {
		url    string
		bucket string
		prefix string
	}{
		{"fake://b", "b", ""},
		{"fake://b/", "b", ""},
		{"fake://b/p/", "b", "p"},
		{"fake://b/p", "b", "p"},
		{"fake://b//a/b/", "b", "a/b"},
		{"fake://b/a/b/c", "b", "a/b/c"},
	}
	for _, tt := range tests {
		repo, prefix, err := Open(t.Context(), tt.url)
		if err != nil {
			t.Errorf("Open(%q): unexpected error %v", tt.url, err)
			continue
		}
		if prefix != tt.prefix {
			t.Errorf("Open(%q): prefix = %q, want %q", tt.url, prefix, tt.prefix)
		}
		if got := repo.(*fakeRepository).bucket; got != tt.bucket {
			t.Errorf("Open(%q): bucket = %q, want %q", tt.url, got, tt.bucket)
		}
	}
}
