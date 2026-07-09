package transfer

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// sha1EntryName is the name of the trailing tar entry that carries the hex
// sha1 digest of the archive payload. It is written last by StreamDir and
// verified (never extracted to disk) by ExtractDir, so a corrupt or truncated
// transfer is rejected before it can become a partition directory.
const sha1EntryName = ".vlbackup-sha1"

// hashEntry folds a regular file's name and contents into h, so the digest
// covers both the payload bytes and the archive layout in archive order. The
// NUL delimiter keeps name/content boundaries unambiguous. StreamDir and
// ExtractDir must call this identically for the digests to match.
func hashEntry(h hash.Hash, name string) {
	_, _ = io.WriteString(h, name)
	_, _ = h.Write([]byte{0})
}

// StreamDir writes a tar.gz of the directory tree rooted at root to w.
// Entries are named relative to root. Only regular files and directories
// are archived; anything else (symlinks, devices...) is an error.
// gzip.BestSpeed is used since VictoriaLogs parts are already compressed.
// A trailing sha1EntryName entry holds the sha1 of the payload for ExtractDir
// to verify; the returned digest is that same hex sum, for logging.
func StreamDir(root string, w io.Writer) (string, error) {
	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return "", err
	}
	tw := tar.NewWriter(gz)
	h := sha1.New()
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
		hashEntry(h, hdr.Name)
		_, err = io.Copy(io.MultiWriter(tw, h), f)
		return err
	})
	if err != nil {
		return "", err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if err := tw.WriteHeader(&tar.Header{
		Name:     sha1EntryName,
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(digest)),
	}); err != nil {
		return "", err
	}
	if _, err := io.WriteString(tw, digest); err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return digest, nil
}

// ExtractDir reads a tar.gz stream from r and extracts it under dest.
// Entries that escape dest (absolute paths, ".." traversal) or that are
// not regular files or directories are rejected. The trailing sha1EntryName
// entry is not written to disk; instead its digest is compared against a sha1
// recomputed over the extracted payload, and a mismatch (or a missing digest
// entry) is an error — so a truncated or corrupt archive never lands on disk.
// Returns the bytes written and the verified hex digest.
func ExtractDir(r io.Reader, dest string) (int64, string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	cleanDest := filepath.Clean(dest)
	h := sha1.New()
	var written int64
	var expected string
	var sawDigest bool
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return written, "", err
		}
		if hdr.Name == sha1EntryName {
			buf, err := io.ReadAll(tr)
			if err != nil {
				return written, "", err
			}
			expected = string(buf)
			sawDigest = true
			continue
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			return written, "", fmt.Errorf("tar entry escapes destination: %q", hdr.Name)
		}
		target := filepath.Join(cleanDest, name)
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return written, "", fmt.Errorf("tar entry escapes destination: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return written, "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return written, "", err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, hdr.FileInfo().Mode().Perm())
			if err != nil {
				return written, "", err
			}
			hashEntry(h, hdr.Name)
			n, err := io.Copy(io.MultiWriter(f, h), tr)
			written += n
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return written, "", err
			}
		default:
			return written, "", fmt.Errorf("unsupported tar entry type %d for %q", hdr.Typeflag, hdr.Name)
		}
	}
	if !sawDigest {
		return written, "", fmt.Errorf("archive missing %s digest entry", sha1EntryName)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != expected {
		return written, "", fmt.Errorf("archive checksum mismatch: got %s, want %s", got, expected)
	}
	return written, got, nil
}
