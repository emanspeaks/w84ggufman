package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type applyUpdatesRequest struct {
	RepoID string `json:"repoId,omitempty"` // if empty, apply all repos with updates
}

func (s *Server) HandleApplyUpdates(w http.ResponseWriter, r *http.Request) {
	if s.deps.HasUpdateAvailable == nil {
		http.Error(w, "update checking not available", http.StatusServiceUnavailable)
		return
	}
	var req applyUpdatesRequest
	if r.ContentLength > 0 {
		json.NewDecoder(r.Body).Decode(&req)
	}

	queued := 0
	entries, err := os.ReadDir(s.cfg.ModelsDir)
	if err != nil {
		http.Error(w, "failed to read models dir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(s.cfg.ModelsDir, entry.Name())
		if s.deps.IsOrgDir != nil && s.deps.IsOrgDir(dirPath) {
			subs, _ := os.ReadDir(dirPath)
			for _, sub := range subs {
				if sub.IsDir() {
					if s.queueRepoUpdate(filepath.Join(dirPath, sub.Name()), req.RepoID) {
						queued++
					}
				}
			}
		} else {
			if s.queueRepoUpdate(dirPath, req.RepoID) {
				queued++
			}
		}
	}
	writeJSON(w, map[string]any{"queued": queued})
}

// queueRepoUpdate queues a re-download for the given repo dir if it has an
// update available (and filterRepoID is empty or matches the repo's repoID).
// Returns true if a download was successfully queued.
func (s *Server) queueRepoUpdate(dir, filterRepoID string) bool {
	if !s.deps.HasUpdateAvailable(dir) {
		return false
	}
	meta := s.deps.ReadModelMeta(dir)
	if meta.RepoID == "" || meta.SkipHFSync {
		return false
	}
	if filterRepoID != "" && meta.RepoID != filterRepoID {
		return false
	}

	// Verify the download would land in the expected directory.
	expectedDir := filepath.Clean(filepath.Join(s.cfg.ModelsDir, filepath.FromSlash(meta.RepoID)))
	if filepath.Clean(dir) != expectedDir {
		return false
	}

	files, _ := s.deps.ScanFilesRelative(dir)
	var mainFiles []string
	var sidecarFiles []string
	seenShardStems := make(map[string]struct{})

	for _, f := range files {
		base := filepath.Base(filepath.FromSlash(f))
		if !strings.HasSuffix(strings.ToLower(base), ".gguf") {
			// Non-gguf tracked file (e.g. VAE safetensors) — treat as sidecar.
			sidecarFiles = append(sidecarFiles, f)
			continue
		}
		if s.deps.MatchesMmproj != nil && s.deps.MatchesMmproj(base) {
			sidecarFiles = append(sidecarFiles, f)
			continue
		}
		// Deduplicate shard sets: only pass the first shard; the downloader's
		// shardPattern() will expand it to a glob matching all shards.
		if shardRe.MatchString(base) {
			stem := shardRe.ReplaceAllString(base, "")
			if _, seen := seenShardStems[stem]; seen {
				continue
			}
			seenShardStems[stem] = struct{}{}
		}
		mainFiles = append(mainFiles, f)
	}

	if len(mainFiles) == 0 && len(sidecarFiles) == 0 {
		return false
	}

	// Use the first main file as the entry point if no main files present.
	dl := mainFiles
	if len(dl) == 0 {
		dl = sidecarFiles
		sidecarFiles = nil
	}

	_, err := s.dl.Start(meta.RepoID, dl, sidecarFiles, 0, false)
	return err == nil
}
