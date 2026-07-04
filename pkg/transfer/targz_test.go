package transfer_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	transfer "github.com/sbordeyne/vlbackup/pkg/transfer"
)

func writeFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStreamExtractRoundTrip(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "datadb", "parts.json"), []byte(`["18A0AD752171BFCD"]`))
	writeFile(t, filepath.Join(src, "datadb", "18A0AD752171BFCD", "index.bin"), []byte{0x00, 0x01, 0xFF, 0xFE})
	writeFile(t, filepath.Join(src, "indexdb", "items.bin"), bytes.Repeat([]byte("abc"), 1000))
	if err := os.MkdirAll(filepath.Join(src, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := transfer.StreamDir(src, &buf); err != nil {
		t.Fatalf("StreamDir: %v", err)
	}

	dest := t.TempDir()
	written, err := transfer.ExtractDir(&buf, dest)
	if err != nil {
		t.Fatalf("ExtractDir: %v", err)
	}
	wantBytes := int64(len(`["18A0AD752171BFCD"]`) + 4 + 3000)
	if written != wantBytes {
		t.Errorf("bytes written = %d, want %d", written, wantBytes)
	}

	for path, want := range map[string][]byte{
		"datadb/parts.json":                 []byte(`["18A0AD752171BFCD"]`),
		"datadb/18A0AD752171BFCD/index.bin": {0x00, 0x01, 0xFF, 0xFE},
		"indexdb/items.bin":                 bytes.Repeat([]byte("abc"), 1000),
	} {
		got, err := os.ReadFile(filepath.Join(dest, path))
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s contents mismatch", path)
		}
	}
	if fi, err := os.Stat(filepath.Join(dest, "emptydir")); err != nil || !fi.IsDir() {
		t.Errorf("emptydir not extracted as directory: %v", err)
	}
}

func TestStreamDirHardlinks(t *testing.T) {
	src := t.TempDir()
	original := filepath.Join(src, "original.bin")
	writeFile(t, original, []byte("hardlinked contents"))
	if err := os.Link(original, filepath.Join(src, "link.bin")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := transfer.StreamDir(src, &buf); err != nil {
		t.Fatalf("StreamDir with hardlinks: %v", err)
	}
	dest := t.TempDir()
	if _, err := transfer.ExtractDir(&buf, dest); err != nil {
		t.Fatalf("ExtractDir: %v", err)
	}
	for _, name := range []string{"original.bin", "link.bin"} {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if string(got) != "hardlinked contents" {
			t.Errorf("%s = %q, want full contents", name, got)
		}
	}
}

func TestStreamDirRejectsSymlinks(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "file.txt"), []byte("x"))
	if err := os.Symlink("file.txt", filepath.Join(src, "sym.txt")); err != nil {
		t.Fatal(err)
	}
	if err := transfer.StreamDir(src, &bytes.Buffer{}); err == nil {
		t.Error("StreamDir accepted a symlink, want error")
	}
}

// buildTar builds a gzipped tar with the given entries for injection tests.
func buildTar(t *testing.T, entries map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, contents := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(contents)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExtractDirRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../evil.txt", "a/../../evil.txt", "/abs/evil.txt"} {
		t.Run(name, func(t *testing.T) {
			buf := buildTar(t, map[string]string{name: "evil"})
			if _, err := transfer.ExtractDir(buf, t.TempDir()); err == nil {
				t.Errorf("ExtractDir accepted entry %q, want error", name)
			}
		})
	}
}

func TestExtractDirRejectsSpecialEntries(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "sym.txt",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
		Mode:     0o777,
	}); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	if _, err := transfer.ExtractDir(&buf, t.TempDir()); err == nil {
		t.Error("ExtractDir accepted a symlink entry, want error")
	}
}

func TestExtractDirRejectsGarbage(t *testing.T) {
	if _, err := transfer.ExtractDir(bytes.NewBufferString("not a gzip stream"), t.TempDir()); err == nil {
		t.Error("ExtractDir accepted garbage input, want error")
	}
}
