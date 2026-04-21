package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
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

	if err := s.dl.Start(req.RepoID, req.Filenames, req.SidecarFiles, req.TotalBytes, req.Force); err != nil {
		log.Printf("error: start download %s %v: %v", req.RepoID, req.Filenames, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"conflict": "busy", "message": err.Error()})
		return
	}
	log.Printf("download queued: %s %v", req.RepoID, req.Filenames)
	w.WriteHeader(http.StatusAccepted)
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

	var errMsgs []string
	for _, f := range req.Files {
		if strings.Contains(f, "..") {
			errMsgs = append(errMsgs, "invalid path: "+f)
			continue
		}
		fullPath := filepath.Clean(filepath.Join(repoDir, filepath.FromSlash(f)))
		if fullPath != repoDir && !strings.HasPrefix(fullPath, repoDir+sep) {
			errMsgs = append(errMsgs, "path traversal: "+f)
			continue
		}
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			base := filepath.Base(fullPath)
			fullPath = findFileByBasename(repoDir, base)
		}
		if fullPath == "" {
			continue
		}
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			log.Printf("warning: delete file %q: %v", fullPath, err)
			errMsgs = append(errMsgs, f+": "+err.Error())
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

func (s *Server) HandleCancelDownload(w http.ResponseWriter, r *http.Request) {
	s.dl.CancelDownload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) HandleDownloadStatus(w http.ResponseWriter, r *http.Request) {
	s.dl.StreamSSE(w, r)
}
