package main

import (
	"io/fs"
	"os"
	"path/filepath"
)

// removeAllWritable chmod-walks path to make every entry owner-writable before
// calling os.RemoveAll. This is necessary because hf download writes files and
// dirs with restrictive permissions (e.g. 0555 dirs, 0444 files) that would
// cause os.RemoveAll to fail with "permission denied" even when the process
// owns the tree.
func removeAllWritable(path string) error {
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			os.Chmod(p, 0755)
		} else {
			os.Chmod(p, 0644)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// dirSize returns the total size of all regular files under path.
func dirSize(path string) int64 {
	var total int64
	filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// isOrgDir reports whether dir contains only subdirectories and no regular
// files, which is the signature of an HF org-level namespace directory.
func isOrgDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			return false
		}
	}
	return true
}

// scanFilesRelative returns all regular files under dir as forward-slash
// relative paths (skipping the w84ggufman metadata file), together with
// the total size in bytes.
func scanFilesRelative(dir string) ([]string, int64) {
	var files []string
	var total int64
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Name() == metaFilename || info.Name() == metaFilenameYAML {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		files = append(files, filepath.ToSlash(rel))
		total += info.Size()
		return nil
	})
	if files == nil {
		files = []string{}
	}
	return files, total
}
