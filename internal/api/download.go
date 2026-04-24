package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *Server) HandleDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepoID       string   `json:"repoId"`
		Filenames    []string `json:"filenames"`
		SidecarFiles []string `json:"sidecarFiles"`
		TotalBytes   int64    `json:"totalBytes"`
		Force        bool     `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.RepoID == "" || len(req.Filenames) == 0 {
		http.Error(w, "repoId and filenames are required", http.StatusBadRequest)
		return
	}

	queued, err := s.dl.Start(req.RepoID, req.Filenames, req.SidecarFiles, req.TotalBytes, req.Force)
	if err != nil {
		log.Printf("error: start download %s %v: %v", req.RepoID, req.Filenames, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if queued {
		log.Printf("download enqueued: %s %v", req.RepoID, req.Filenames)
	} else {
		log.Printf("download started: %s %v", req.RepoID, req.Filenames)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{"queued": queued})
}

func (s *Server) HandleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id parameter", http.StatusBadRequest)
		return
	}
	if strings.Contains(id, "..") {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var repoDir string
	if filepath.IsAbs(id) {
		clean := filepath.Clean(id)
		if !strings.HasPrefix(clean, filepath.Clean(s.cfg.ModelsDir)+string(filepath.Separator)) {
			http.Error(w, "path not under models dir", http.StatusBadRequest)
			return
		}
		repoDir = clean
	} else {
		repoDir = filepath.Join(s.cfg.ModelsDir, filepath.FromSlash(id))
	}

	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		http.Error(w, "repo not found", http.StatusNotFound)
		return
	}
	if err := s.deps.RemoveAllWritable(repoDir); err != nil {
		log.Printf("error: delete repo %q: %v", id, err)
		http.Error(w, "failed to delete: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("deleted repo dir %q", repoDir)

	parentDir := filepath.Dir(repoDir)
	if parentDir != filepath.Clean(s.cfg.ModelsDir) {
		if entries, _ := os.ReadDir(parentDir); len(entries) == 0 {
			if err := os.Remove(parentDir); err != nil {
				log.Printf("warning: could not remove empty org dir %q: %v", parentDir, err)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func findFileByBasename(dir, target string) string {
	var found string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != "" {
			return nil
		}
		if info.Name() == target {
			found = path
		}
		return nil
	})
	return found
}

func (s *Server) HandleDeleteFiles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepoID string   `json:"repoId"`
		Files  []string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.RepoID == "" || len(req.Files) == 0 {
		http.Error(w, "repoId and files are required", http.StatusBadRequest)
		return
	}
	if strings.Contains(req.RepoID, "..") {
		http.Error(w, "invalid repoId", http.StatusBadRequest)
		return
	}

	repoDir := filepath.Clean(filepath.Join(s.cfg.ModelsDir, filepath.FromSlash(req.RepoID)))
	sep := string(filepath.Separator)

	// Resolve the requested files and expand any shard sets so that all parts
	// of a multi-part model are deleted together.
	toDelete := make(map[string]struct{})
	var errMsgs []string
	for _, f := range req.Files {
		if strings.Contains(f, "..") {
			errMsgs = append(errMsgs, "invalid path: "+f)
			continue
		}
		origFull := filepath.Clean(filepath.Join(repoDir, filepath.FromSlash(f)))
		if origFull != repoDir && !strings.HasPrefix(origFull, repoDir+sep) {
			errMsgs = append(errMsgs, "path traversal: "+f)
			continue
		}
		base := filepath.Base(origFull)

		if shardRe.MatchString(base) {
			// For sharded files always expand using the directory from the original
			// request path. Never fall back to findFileByBasename here — a basename
			// search ignores directory context and would match the same shard number
			// from a different quant's subdirectory (e.g. Q8_0/model-00002 instead
			// of Q5_K_M/model-00002).
			stem := shardRe.ReplaceAllString(base, "")
			scanDir := filepath.Dir(origFull)
			if entries, err := os.ReadDir(scanDir); err == nil {
				for _, e := range entries {
					if !e.IsDir() && shardRe.MatchString(e.Name()) {
						if shardRe.ReplaceAllString(e.Name(), "") == stem {
							toDelete[filepath.Join(scanDir, e.Name())] = struct{}{}
						}
					}
				}
			}
			continue
		}

		// Non-shard: resolve and delete the single file.
		fullPath := origFull
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			fullPath = findFileByBasename(repoDir, base)
		}
		if fullPath == "" {
			continue
		}
		toDelete[fullPath] = struct{}{}
	}

	for fullPath := range toDelete {
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			log.Printf("warning: delete file %q: %v", fullPath, err)
			errMsgs = append(errMsgs, filepath.Base(fullPath)+": "+err.Error())
		} else {
			log.Printf("deleted file %q", fullPath)
		}
	}

	if len(errMsgs) > 0 {
		http.Error(w, strings.Join(errMsgs, "; "), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) HandleRemoveFromQueue(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	s.dl.RemoveFromQueue(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) HandleCancelDownload(w http.ResponseWriter, r *http.Request) {
	s.dl.CancelDownload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) HandleDownloadStatus(w http.ResponseWriter, r *http.Request) {
	s.dl.StreamSSE(w, r)
}
