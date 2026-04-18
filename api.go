package main

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/emanspeaks/w84ggufman/internal/ini"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

type server struct {
	cfg    Config
	dl     *downloader
	preset *presetManager
}

func newServer(cfg Config, dl *downloader, pm *presetManager) *server {
	return &server{cfg: cfg, dl: dl, preset: pm}
}

type diskInfo struct {
	TotalBytes uint64 `json:"totalBytes"`
	FreeBytes  uint64 `json:"freeBytes"`
	UsedBytes  uint64 `json:"usedBytes"`
}

func getDiskInfo(path string) (diskInfo, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return diskInfo{}, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	return diskInfo{
		TotalBytes: total,
		FreeBytes:  free,
		UsedBytes:  total - free,
	}, nil
}

type localModel struct {
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	SizeBytes   int64             `json:"sizeBytes"`
	Files       []string          `json:"files"`
	Loaded      bool              `json:"loaded"`
	IsVision    bool              `json:"isVision"`
	Mmproj      string            `json:"mmproj"`
	InPreset    bool              `json:"inPreset"`
	PresetEntry map[string]string `json:"presetEntry,omitempty"`
	RepoID      string            `json:"repoId,omitempty"`
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

	var presetFile *ini.File
	presetFile, _ = s.preset.Load()

	models := make([]localModel, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		modelDir := filepath.Join(s.cfg.ModelsDir, entry.Name())
		var files []string
		var totalSize int64
		var mmprojName string
		filepath.WalkDir(modelDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.HasSuffix(d.Name(), ".gguf") {
				if info, err := d.Info(); err == nil {
					totalSize += info.Size()
				}
				files = append(files, d.Name())
				if matchesMmproj(d.Name()) {
					mmprojName = d.Name()
				}
			}
			return nil
		})
		if len(files) == 0 {
			continue
		}
		_, loaded := loadedModels[entry.Name()]

		inPreset := false
		var presetEntry map[string]string
		if presetFile != nil {
			if sec, ok := presetFile.Sections[entry.Name()]; ok {
				inPreset = true
				presetEntry = sec
			}
		}

		repoID := readModelMeta(modelDir).RepoID
		if repoID == "" {
			// Try to recover the source repo from GGUF file metadata.
			repoID = detectRepoIDFromGGUF(modelDir, files)
			if repoID != "" {
				// Cache it so future lookups are instant.
				_ = writeModelMeta(modelDir, repoID)
			}
		}

		models = append(models, localModel{
			Name:        entry.Name(),
			Path:        modelDir,
			SizeBytes:   totalSize,
			Files:       files,
			Loaded:      loaded,
			IsVision:    mmprojName != "",
			Mmproj:      mmprojName,
			InPreset:    inPreset,
			PresetEntry: presetEntry,
			RepoID:      repoID,
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
	if err := s.preset.RemoveModel(name); err != nil {
		log.Printf("warning: failed to remove %s from managed.ini: %v", name, err)
	}
	if err := restartService(s.cfg.LlamaService); err != nil {
		log.Printf("warning: failed to restart %s: %v", s.cfg.LlamaService, err)
	}
	w.WriteHeader(http.StatusNoContent)
}

type statusResponse struct {
	LlamaReachable     bool     `json:"llamaReachable"`
	DownloadInProgress bool     `json:"downloadInProgress"`
	ActiveDownload     string   `json:"activeDownload"`
	Version            string   `json:"version"`
	Disk               diskInfo `json:"disk"`
	WarnDownloadBytes  uint64   `json:"warnDownloadBytes"`
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
	disk, _ := getDiskInfo(s.cfg.ModelsDir)
	warnBytes := uint64(s.cfg.WarnDownloadGiB * 1024 * 1024 * 1024)
	writeJSON(w, statusResponse{
		LlamaReachable:     reachable,
		DownloadInProgress: inProgress,
		ActiveDownload:     active,
		Version:            version,
		Disk:               disk,
		WarnDownloadBytes:  warnBytes,
	})
}

var mdRenderer = goldmark.New(goldmark.WithExtensions(extension.GFM))

func (s *server) handleReadme(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("id")
	if repoID == "" {
		http.Error(w, "missing id parameter", http.StatusBadRequest)
		return
	}
	if strings.Count(repoID, "/") != 1 || strings.Contains(repoID, "..") || strings.ContainsAny(repoID, " \t\n") {
		http.Error(w, "invalid repo id", http.StatusBadRequest)
		return
	}
	url := "https://huggingface.co/" + repoID + "/resolve/main/README.md"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if s.cfg.HFToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.HFToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "failed to fetch readme: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "HuggingFace returned non-OK status", http.StatusBadGateway)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read readme", http.StatusInternalServerError)
		return
	}
	raw = stripFrontmatter(raw)
	var buf bytes.Buffer
	if err := mdRenderer.Convert(raw, &buf); err != nil {
		http.Error(w, "failed to render readme", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

// stripFrontmatter removes the YAML ---...--- block at the top of model cards.
func stripFrontmatter(b []byte) []byte {
	if !bytes.HasPrefix(b, []byte("---")) {
		return b
	}
	end := bytes.Index(b[3:], []byte("\n---"))
	if end < 0 {
		return b
	}
	rest := b[3+end+4:]
	return bytes.TrimLeft(rest, "\n")
}

func (s *server) handleRepo(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("id")
	if repoID == "" {
		http.Error(w, "missing id parameter", http.StatusBadRequest)
		return
	}
	info, err := fetchRepoInfo(repoID, s.cfg.HFToken)
	if err != nil {
		http.Error(w, "failed to fetch repo: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, info)
}

func (s *server) handleDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepoID       string   `json:"repoId"`
		Filename     string   `json:"filename"`
		SidecarFiles []string `json:"sidecarFiles"`
		TotalBytes   int64    `json:"totalBytes"`
		Force        bool     `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.RepoID == "" || req.Filename == "" {
		http.Error(w, "repoId and filename are required", http.StatusBadRequest)
		return
	}

	if !req.Force {
		modelName := modelNameFromFilename(req.Filename)
		if modelName != "" {
			destDir := filepath.Join(s.cfg.ModelsDir, modelName)
			if _, err := os.Stat(destDir); err == nil {
				existingRepoID := readModelMeta(destDir).RepoID
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{
					"conflict":       "exists",
					"modelName":      modelName,
					"existingRepoId": existingRepoID,
				})
				return
			}
		}
	}

	if err := s.dl.start(req.RepoID, req.Filename, req.SidecarFiles, req.TotalBytes, req.Force); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	s.dl.cancelDownload()
	w.WriteHeader(http.StatusNoContent)
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

func (s *server) handleGetPreset(w http.ResponseWriter, r *http.Request) {
	f, err := s.preset.Load()
	if err != nil {
		http.Error(w, "failed to load preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	type presetResponse struct {
		Global   map[string]string            `json:"global"`
		Sections map[string]map[string]string `json:"sections"`
	}
	writeJSON(w, presetResponse{Global: f.Global, Sections: f.Sections})
}

func (s *server) handleUpdatePresetGlobal(w http.ResponseWriter, r *http.Request) {
	var kvs map[string]string
	if err := json.NewDecoder(r.Body).Decode(&kvs); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.preset.UpdateGlobal(kvs); err != nil {
		http.Error(w, "failed to update preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleUpdatePresetModel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	var kvs map[string]string
	if err := json.NewDecoder(r.Body).Decode(&kvs); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	f, err := s.preset.Load()
	if err != nil {
		http.Error(w, "failed to load preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if f.Sections[name] == nil {
		f.Sections[name] = make(map[string]string)
	}
	for k, v := range kvs {
		f.Sections[name][k] = v
	}
	if err := s.preset.Save(f); err != nil {
		http.Error(w, "failed to save preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
