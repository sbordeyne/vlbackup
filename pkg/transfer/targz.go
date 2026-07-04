package transfer

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// StreamDir writes a tar.gz of the directory tree rooted at root to w.
// Entries are named relative to root. Only regular files and directories
// are archived; anything else (symlinks, devices...) is an error.
// gzip.BestSpeed is used since VictoriaLogs parts are already compressed.
func StreamDir(root string, w io.Writer) error {
	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			hdr.Name += "/"
			return tw.WriteHeader(hdr)
		}
		// Hardlinked snapshot files stat as regular files, so their full
		// contents are archived here — no TypeLink handling needed.
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type in %s: %s", path, info.Mode())
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// ExtractDir reads a tar.gz stream from r and extracts it under dest.
// Entries that escape dest (absolute paths, ".." traversal) or that are
// not regular files or directories are rejected. Returns bytes written.
func ExtractDir(r io.Reader, dest string) (int64, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	cleanDest := filepath.Clean(dest)
	var written int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return written, nil
		}
		if err != nil {
			return written, err
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			return written, fmt.Errorf("tar entry escapes destination: %q", hdr.Name)
		}
		target := filepath.Join(cleanDest, name)
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return written, fmt.Errorf("tar entry escapes destination: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return written, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return written, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, hdr.FileInfo().Mode().Perm())
			if err != nil {
				return written, err
			}
			n, err := io.Copy(f, tr)
			written += n
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return written, err
			}
		default:
			return written, fmt.Errorf("unsupported tar entry type %d for %q", hdr.Typeflag, hdr.Name)
		}
	}
}
