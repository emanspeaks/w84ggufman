package main

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type server struct {
	cfg Config
	dl  *downloader
}

func newServer(cfg Config, dl *downloader) *server {
	return &server{cfg: cfg, dl: dl}
}

type localModel struct {
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	SizeBytes int64    `json:"sizeBytes"`
	Files     []string `json:"files"`
	Loaded    bool     `json:"loaded"`
}

func (s *server) handleLocal(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.cfg.ModelsDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, []localModel{})
			return
		}
		http.Error(w, "failed to read models dir: "+err.Error(), http.StatusInternalServerError)
		return
	}

	loadedModels, _ := s.fetchLoadedModels()

	models := make([]localModel, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		modelDir := filepath.Join(s.cfg.ModelsDir, entry.Name())
		var files []string
		var totalSize int64
		filepath.WalkDir(modelDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.HasSuffix(d.Name(), ".gguf") {
				if info, err := d.Info(); err == nil {
					totalSize += info.Size()
				}
				files = append(files, d.Name())
			}
			return nil
		})
		if len(files) == 0 {
			continue
		}
		_, loaded := loadedModels[entry.Name()]
		models = append(models, localModel{
			Name:      entry.Name(),
			Path:      modelDir,
			SizeBytes: totalSize,
			Files:     files,
			Loaded:    loaded,
		})
	}
	writeJSON(w, models)
}

type llamaModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (s *server) fetchLoadedModels() (map[string]struct{}, error) {
	resp, err := http.Get(s.cfg.LlamaServerURL + "/v1/models")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var lmr llamaModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&lmr); err != nil {
		return nil, err
	}
	loaded := make(map[string]struct{}, len(lmr.Data))
	for _, m := range lmr.Data {
		loaded[m.ID] = struct{}{}
	}
	return loaded, nil
}

func (s *server) handleDeleteLocal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	modelDir := filepath.Join(s.cfg.ModelsDir, name)
	if _, err := os.Stat(modelDir); os.IsNotExist(err) {
		http.Error(w, "model not found", http.StatusNotFound)
		return
	}
	if err := os.RemoveAll(modelDir); err != nil {
		http.Error(w, "failed to delete: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := restartService(s.cfg.LlamaService); err != nil {
		log.Printf("warning: failed to restart %s: %v", s.cfg.LlamaService, err)
	}
	w.WriteHeader(http.StatusNoContent)
}

type statusResponse struct {
	LlamaReachable     bool   `json:"llamaReachable"`
	DownloadInProgress bool   `json:"downloadInProgress"`
	ActiveDownload     string `json:"activeDownload"`
	Version            string `json:"version"`
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	reachable := true
	resp, err := http.Get(s.cfg.LlamaServerURL + "/v1/models")
	if err != nil {
		reachable = false
	} else {
		resp.Body.Close()
	}
	active, inProgress := s.dl.activeInfo()
	writeJSON(w, statusResponse{
		LlamaReachable:     reachable,
		DownloadInProgress: inProgress,
		ActiveDownload:     active,
		Version:            version,
	})
}

func (s *server) handleRepo(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("id")
	if repoID == "" {
		http.Error(w, "missing id parameter", http.StatusBadRequest)
		return
	}
	files, err := fetchRepoFiles(repoID, s.cfg.HFToken)
	if err != nil {
		http.Error(w, "failed to fetch repo: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, files)
}

func (s *server) handleDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepoID   string `json:"repoId"`
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.RepoID == "" || req.Filename == "" {
		http.Error(w, "repoId and filename are required", http.StatusBadRequest)
		return
	}
	if err := s.dl.start(req.RepoID, req.Filename); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) handleDownloadStatus(w http.ResponseWriter, r *http.Request) {
	s.dl.streamSSE(w, r)
}

func (s *server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if err := restartService(s.cfg.LlamaService); err != nil {
		http.Error(w, "failed to restart service: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
