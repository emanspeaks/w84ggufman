package api

import (
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
)

type diskUsageFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type diskUsageResponse struct {
	TotalBytes     uint64          `json:"totalBytes"`
	FreeBytes      uint64          `json:"freeBytes"`
	UsedBytes      uint64          `json:"usedBytes"`
	ModelsDir      string          `json:"modelsDir"`
	ModelsDirBytes uint64          `json:"modelsDirBytes"`
	SystemBytes    uint64          `json:"systemBytes"`
	Files          []diskUsageFile `json:"files"`
}

func (s *Server) HandleDiskUsage(w http.ResponseWriter, r *http.Request) {
	disk, _ := getDiskInfo(s.cfg.ModelsDir)

	var ignorePatterns []string
	if s.deps.EffectiveRootIgnorePattern != nil {
		ignorePatterns = s.deps.EffectiveRootIgnorePattern(s.cfg)
	}

	files := []diskUsageFile{}
	var allModelsTotal uint64
	var displayedTotal uint64

	filepath.WalkDir(s.cfg.ModelsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		size := info.Size()
		if size <= 0 {
			return nil
		}
		rel, rerr := filepath.Rel(s.cfg.ModelsDir, path)
		if rerr != nil {
			rel = path
		}
		relSlash := filepath.ToSlash(rel)
		allModelsTotal += uint64(size)
		if isSystemFile(relSlash, ignorePatterns, s.deps.IsIgnoredEntry) {
			return nil
		}
		files = append(files, diskUsageFile{Path: relSlash, Size: size})
		displayedTotal += uint64(size)
		return nil
	})

	// system = OS/non-models disk usage + hidden/config files filtered from models dir
	var system uint64
	if disk.UsedBytes > allModelsTotal {
		system = disk.UsedBytes - allModelsTotal
	}
	system += allModelsTotal - displayedTotal

	writeJSON(w, diskUsageResponse{
		TotalBytes:     disk.TotalBytes,
		FreeBytes:      disk.FreeBytes,
		UsedBytes:      disk.UsedBytes,
		ModelsDir:      s.cfg.ModelsDir,
		ModelsDirBytes: displayedTotal,
		SystemBytes:    system,
		Files:          files,
	})
}

// isSystemFile returns true for files that should be lumped into the system
// block rather than shown as individual entries in the treemap: hidden files,
// known config filenames, and files matching the root ignore patterns.
func isSystemFile(rel string, ignorePatterns []string, isIgnoredFn func(string, []string, bool, bool) bool) bool {
	parts := strings.Split(rel, "/")
	base := parts[len(parts)-1]
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	lower := strings.ToLower(base)
	if lower == "config.yaml" || lower == "config.yml" || lower == "models.ini" {
		return true
	}
	if isIgnoredFn != nil && isIgnoredFn(base, ignorePatterns, false, false) {
		return true
	}
	return false
}
