package objstore

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
)

// Factory builds a bucket-scoped Repository from a parsed destination URL.
// The URL host is the bucket name.
type Factory func(ctx context.Context, u *url.URL) (Repository, error)

var registry = map[string]Factory{}

// Register makes a backend available under the given URL scheme.
// It is meant to be called from init() in each backend file.
func Register(scheme string, f Factory) {
	registry[scheme] = f
}

// Open parses rawURL, dispatches on its scheme, and returns a bucket-scoped
// Repository plus the normalized key prefix from the URL path
// (leading/trailing slashes trimmed; "" means bucket root).
func Open(ctx context.Context, rawURL string) (Repository, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", err
	}
	f, ok := registry[u.Scheme]
	if !ok {
		return nil, "", fmt.Errorf("%w %q (supported: %s)", ErrUnsupportedScheme, u.Scheme, strings.Join(slices.Sorted(maps.Keys(registry)), ", "))
	}
	if u.Host == "" {
		return nil, "", fmt.Errorf("destination URL %q has no bucket", rawURL)
	}
	repo, err := f(ctx, u)
	if err != nil {
		return nil, "", err
	}
	return repo, strings.Trim(u.Path, "/"), nil
}
