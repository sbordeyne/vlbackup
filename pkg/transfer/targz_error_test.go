package transfer

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestStreamDirNonexistentRoot(t *testing.T) {
	if err := StreamDir(filepath.Join(t.TempDir(), "does-not-exist"), io.Discard); err == nil {
		t.Error("StreamDir on missing root err = nil, want error")
	}
}

// TestExtractDirTarError feeds a valid gzip stream whose decompressed content
// is not a valid tar archive, so tar.Reader.Next returns a non-EOF error.
func TestExtractDirTarError(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("this is valid gzip but not a tar archive at all"))
	_ = gz.Close()
	if _, err := ExtractDir(&buf, t.TempDir()); err == nil {
		t.Error("ExtractDir on non-tar gzip err = nil, want error")
	}
}

// TestExtractDirOpenFileError makes the target path a pre-existing directory,
// so OpenFile on the regular-file entry fails.
func TestExtractDirOpenFileError(t *testing.T) {
	dest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dest, "foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	buf := buildTar(t, map[string]string{"foo": "contents"})
	if _, err := ExtractDir(buf, dest); err == nil {
		t.Error("ExtractDir over existing dir err = nil, want error")
	}
}
