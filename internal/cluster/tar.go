package cluster

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxSnapshotTarFileBytes int64 = 10 << 30

// tarDir writes the contents of dir as a tar archive to w.
func tarDir(dir string, w io.Writer) error {
	tw := tar.NewWriter(w)
	defer tw.Close()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("opening tar root: %w", err)
	}
	defer root.Close()

	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("creating tar header: %w", err)
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("writing tar header: %w", err)
		}

		if info.IsDir() {
			return nil
		}

		f, err := root.Open(rel)
		if err != nil {
			return fmt.Errorf("opening file for tar: %w", err)
		}

		if _, err := io.Copy(tw, f); err != nil {
			if closeErr := f.Close(); closeErr != nil {
				return fmt.Errorf("closing file after tar copy failure: %w", closeErr)
			}
			return fmt.Errorf("copying file to tar: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("closing file after tar copy: %w", err)
		}

		return nil
	})
}

// untar extracts a tar archive from r into dir.
func untar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("opening untar root: %w", err)
	}
	defer root.Close()

	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading tar header: %w", err)
		}

		target := filepath.Clean(header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(target, 0o750); err != nil {
				return fmt.Errorf("creating directory: %w", err)
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > maxSnapshotTarFileBytes {
				return fmt.Errorf("invalid tar file size for %s: %d", header.Name, header.Size)
			}
			if err := root.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return fmt.Errorf("creating parent directory: %w", err)
			}
			mode := os.FileMode(header.Mode & 0o777)
			f, err := root.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return fmt.Errorf("creating file: %w", err)
			}
			if _, err := io.CopyN(f, tr, header.Size); err != nil {
				if closeErr := f.Close(); closeErr != nil {
					return fmt.Errorf("closing file after write failure: %w", closeErr)
				}
				return fmt.Errorf("writing file: %w", err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("closing file: %w", err)
			}
		}
	}
}
