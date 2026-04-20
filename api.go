package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

type server struct {
	cfg       Config
	dl        *downloader
	preset    *presetManager
	llamaSwap *llamaSwapManager
	vramBytes uint64
}

func newServer(cfg Config, dl *downloader, pm *presetManager, lsm *llamaSwapManager) *server {
	vram := uint64(cfg.VramGiB * 1024 * 1024 * 1024)
	if vram == 0 {
		vram = detectVRAMBytes()
	}
	return &server{cfg: cfg, dl: dl, preset: pm, llamaSwap: lsm, vramBytes: vram}
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

// modelMetaParentDir returns the direct child of modelsDir that is an ancestor
// of (or equal to) dir. This is the canonical location for .w84ggufman.json,
// regardless of how deeply nested the actual model files are. Returns "" when
// dir is not under modelsDir.
func modelMetaParentDir(dir, modelsDir string) string {
	dir = filepath.Clean(dir)
	modelsDir = filepath.Clean(modelsDir)
	prev := dir
	for {
		parent := filepath.Dir(prev)
		if parent == modelsDir {
			return prev
		}
		if parent == prev {
			return "" // reached fs root without crossing modelsDir
		}
		prev = parent
	}
}

// iniModelDir returns the directory that contains (or IS) the model for an INI
// section. When the "model" key points to a .gguf file, returns its parent dir.
// When it points to a directory (sharded model or bare quant dir), returns the
// path itself. Returns "" when the key is absent.
func iniModelDir(section map[string]string) string {
	p := section["model"]
	if p == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(p), ".gguf") {
		return filepath.Dir(p)
	}
	// Path has no .gguf extension — llama.cpp accepts a directory for sharded
	// models, so treat the value as the model directory directly.
	return p
}

// scanModelDir returns all non-mmproj .gguf filenames and their total size in
// dir. Returns a non-nil empty slice (not nil) so JSON encodes as [] not null.
func scanModelDir(dir string) (files []string, totalSize int64) {
	files = []string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("scanModelDir %q: %v", dir, err)
		}
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gguf") || matchesMmproj(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil {
			totalSize += info.Size()
		}
		files = append(files, e.Name())
	}
	return
}

func (s *server) handleLocal(w http.ResponseWriter, r *http.Request) {
	loadedModels, _ := s.fetchLoadedModels()
	presetFile, _ := s.preset.Load()

	models := make([]localModel, 0)
	coveredDirs := make(map[string]struct{})

	// Primary: one card per INI section (sorted for stable output).
	if presetFile != nil {
		names := make([]string, 0, len(presetFile.Sections))
		for n := range presetFile.Sections {
			names = append(names, n)
		}
		sort.Strings(names)

		for _, name := range names {
			section := presetFile.Sections[name]
			modelDir := iniModelDir(section)
			if modelDir != "" {
				coveredDirs[filepath.Clean(modelDir)] = struct{}{}
			}

			files := []string{}
			var totalSize int64
			if modelDir != "" {
				files, totalSize = scanModelDir(modelDir)
			} else {
				log.Printf("handleLocal: INI section %q has no model path", name)
			}

			mmprojPath := section["mmproj"]
			mmprojName := ""
			if mmprojPath != "" {
				mmprojName = filepath.Base(mmprojPath)
			}

			// Walk up from modelDir toward ModelsDir to find .w84ggufman.json.
			// This handles flat, one-level-nested, and doubly-nested layouts.
			repoID := ""
			if modelDir != "" {
				for dir := filepath.Clean(modelDir); dir != s.cfg.ModelsDir && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
					if meta := readModelMeta(dir); meta.RepoID != "" {
						repoID = meta.RepoID
						break
					}
				}
				if repoID == "" && len(files) > 0 {
					repoID = detectRepoIDFromGGUF(modelDir, files)
					if repoID != "" {
						// Write to the model parent dir (direct child of ModelsDir),
						// not the quant subdir, so it survives individual quant deletion.
						writeDir := modelMetaParentDir(modelDir, s.cfg.ModelsDir)
						if writeDir == "" {
							writeDir = modelDir
						}
						_ = writeModelMeta(writeDir, repoID)
					}
				}
			}

			_, loaded := loadedModels[name]
			models = append(models, localModel{
				Name:        name,
				Path:        modelDir,
				SizeBytes:   totalSize,
				Files:       files,
				Loaded:      loaded,
				IsVision:    mmprojPath != "",
				Mmproj:      mmprojName,
				InPreset:    true,
				PresetEntry: section,
				RepoID:      repoID,
			})
		}
	}

	// Secondary: llamaswap config.yaml entries not already covered by INI.
	// This ensures models that are registered in config.yaml but absent from
	// models.ini get their proper name instead of the subdirectory name.
	if s.llamaSwap != nil {
		if lsModels, err := s.llamaSwap.ListModels(); err == nil {
			for _, lsm := range lsModels {
				if lsm.ModelPath == "" {
					continue
				}
				modelDir := filepath.Dir(lsm.ModelPath)
				if _, covered := coveredDirs[filepath.Clean(modelDir)]; covered {
					continue
				}
				coveredDirs[filepath.Clean(modelDir)] = struct{}{}

				files, totalSize := scanModelDir(modelDir)
				_, loaded := loadedModels[lsm.Name]

				repoID := ""
				for dir := filepath.Clean(modelDir); dir != s.cfg.ModelsDir && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
					if meta := readModelMeta(dir); meta.RepoID != "" {
						repoID = meta.RepoID
						break
					}
				}
				if repoID == "" && len(files) > 0 {
					repoID = detectRepoIDFromGGUF(modelDir, files)
					if repoID != "" {
						writeDir := modelMetaParentDir(modelDir, s.cfg.ModelsDir)
						if writeDir == "" {
							writeDir = modelDir
						}
						_ = writeModelMeta(writeDir, repoID)
					}
				}

				mmprojName := ""
				if lsm.MmprojPath != "" {
					mmprojName = filepath.Base(lsm.MmprojPath)
				}

				models = append(models, localModel{
					Name:      lsm.Name,
					Path:      modelDir,
					SizeBytes: totalSize,
					Files:     files,
					Loaded:    loaded,
					IsVision:  !lsm.IsSD && lsm.MmprojPath != "",
					Mmproj:    mmprojName,
					InPreset:  true,
					RepoID:    repoID,
				})
			}
		}
	}

	// Fallback: scan directories for models not covered by any INI section.
	// Handles manually placed models and the old flat layout.
	if dirEntries, err := os.ReadDir(s.cfg.ModelsDir); err == nil {
		for _, entry := range dirEntries {
			if !entry.IsDir() {
				continue
			}
			parentDir := filepath.Join(s.cfg.ModelsDir, entry.Name())

			// New nested layout: check quant subdirs.
			subEntries, _ := os.ReadDir(parentDir)
			mmprojFile := ""
			for _, sub := range subEntries {
				if !sub.IsDir() && strings.HasSuffix(sub.Name(), ".gguf") && matchesMmproj(sub.Name()) {
					mmprojFile = sub.Name()
					break
				}
			}
			for _, sub := range subEntries {
				if !sub.IsDir() {
					continue
				}
				quantDir := filepath.Join(parentDir, sub.Name())
				if _, covered := coveredDirs[filepath.Clean(quantDir)]; covered {
					continue
				}
				files, totalSize := scanModelDir(quantDir)
				if len(files) == 0 {
					continue
				}
				name := sub.Name()
				_, loaded := loadedModels[name]
				repoID := readModelMeta(parentDir).RepoID
				if repoID == "" {
					repoID = detectRepoIDFromGGUF(quantDir, files)
					if repoID != "" {
						_ = writeModelMeta(parentDir, repoID)
					}
				}
				models = append(models, localModel{
					Name:      name,
					Path:      quantDir,
					SizeBytes: totalSize,
					Files:     files,
					Loaded:    loaded,
					IsVision:  mmprojFile != "",
					Mmproj:    mmprojFile,
					InPreset:  false,
					RepoID:    repoID,
				})
			}

			// Old flat layout: model files directly in parentDir.
			if _, covered := coveredDirs[filepath.Clean(parentDir)]; covered {
				continue
			}
			files, totalSize := scanModelDir(parentDir)
			if len(files) == 0 {
				continue
			}
			name := entry.Name()
			_, loaded := loadedModels[name]
			repoID := readModelMeta(parentDir).RepoID
			if repoID == "" {
				repoID = detectRepoIDFromGGUF(parentDir, files)
				if repoID != "" {
					_ = writeModelMeta(parentDir, repoID)
				}
			}
			models = append(models, localModel{
				Name:      name,
				Path:      parentDir,
				SizeBytes: totalSize,
				Files:     files,
				Loaded:    loaded,
				IsVision:  mmprojFile != "",
				Mmproj:    mmprojFile,
				InPreset:  false,
				RepoID:    repoID,
			})
		}
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

// cleanupEmptyParentDir removes parentDir if it contains no more quant subdirs
// with model .gguf files. Safe to call after deleting the last quant in a parent.
func (s *server) cleanupEmptyParentDir(parentDir string) {
	if parentDir == "" || parentDir == s.cfg.ModelsDir {
		return
	}
	entries, _ := os.ReadDir(parentDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subFiles, _ := os.ReadDir(filepath.Join(parentDir, e.Name()))
		for _, f := range subFiles {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".gguf") && !matchesMmproj(f.Name()) {
				return // still has quants
			}
		}
	}
	if err := removeAllWritable(parentDir); err != nil {
		log.Printf("warning: could not remove empty model parent dir %q: %v", parentDir, err)
	}
}

func (s *server) handleDeleteLocal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}

	// Determine model dir: prefer INI lookup (works for both layouts), fall back
	// to directory search for models not registered in models.ini.
	modelDir := ""
	fromINI := false
	if pf, err := s.preset.Load(); err == nil {
		if sec, ok := pf.Sections[name]; ok {
			modelDir = iniModelDir(sec)
			fromINI = true
		}
	}
	if modelDir == "" {
		// Fallback: search directories (old flat layout or unregistered models).
		if parents, err := os.ReadDir(s.cfg.ModelsDir); err == nil {
			for _, p := range parents {
				if !p.IsDir() {
					continue
				}
				candidate := filepath.Join(s.cfg.ModelsDir, p.Name(), name)
				if info, err := os.Stat(candidate); err == nil && info.IsDir() {
					modelDir = candidate
					break
				}
			}
		}
		if modelDir == "" {
			modelDir = filepath.Join(s.cfg.ModelsDir, name)
		}
	}

	if _, err := os.Stat(modelDir); os.IsNotExist(err) {
		if !fromINI {
			http.Error(w, "model not found", http.StatusNotFound)
			return
		}
		// INI section exists but directory already gone — still clean up the INI.
	} else {
		if err := removeAllWritable(modelDir); err != nil {
			log.Printf("error: delete model %q: %v", name, err)
			http.Error(w, "failed to delete: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("deleted model %q", name)
		// Remove parent dir if it now has no remaining quant subdirs.
		parentDir := filepath.Dir(modelDir)
		s.cleanupEmptyParentDir(parentDir)
	}

	if err := s.preset.RemoveModel(name); err != nil {
		log.Printf("warning: failed to remove %s from models.ini: %v", name, err)
	}
	if s.llamaSwap != nil {
		if err := s.llamaSwap.RemoveModel(name); err != nil {
			log.Printf("warning: failed to remove %s from config.yaml: %v", name, err)
		}
	}
	if s.llamaSwap == nil || s.cfg.ForceRestartOnLlamaSwap {
		if err := restartService(s.cfg.LlamaService); err != nil {
			log.Printf("warning: failed to restart %s: %v", s.cfg.LlamaService, err)
		}
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
	VramBytes          uint64   `json:"vramBytes"`
	VramUsedBytes      uint64   `json:"vramUsedBytes"`
	VramUsedKnown      bool     `json:"vramUsedKnown"`
	WarnVramBytes      uint64   `json:"warnVramBytes"`
	LoadedModels       []string `json:"loadedModels"`
	LlamaSwapEnabled   bool     `json:"llamaSwapEnabled"`
	LlamaServiceLabel  string   `json:"llamaServiceLabel"`
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	reachable := true
	var loadedIDs []string
	resp, err := http.Get(s.cfg.LlamaServerURL + "/v1/models")
	if err != nil {
		reachable = false
	} else {
		var lmr llamaModelsResponse
		if err := json.NewDecoder(resp.Body).Decode(&lmr); err == nil {
			for _, m := range lmr.Data {
				loadedIDs = append(loadedIDs, m.ID)
			}
		}
		resp.Body.Close()
	}
	active, inProgress := s.dl.activeInfo()
	disk, _ := getDiskInfo(s.cfg.ModelsDir)
	warnBytes := uint64(s.cfg.WarnDownloadGiB * 1024 * 1024 * 1024)
	pct := s.cfg.WarnVramPercent
	if pct <= 0 {
		pct = 80
	}
	warnVram := uint64(float64(s.vramBytes) * pct / 100)
	writeJSON(w, statusResponse{
		LlamaReachable:     reachable,
		DownloadInProgress: inProgress,
		ActiveDownload:     active,
		Version:            version,
		Disk:               disk,
		WarnDownloadBytes:  warnBytes,
		VramBytes:          s.vramBytes,
		VramUsedBytes:      0,
		VramUsedKnown:      false,
		WarnVramBytes:      warnVram,
		LoadedModels:       loadedIDs,
		LlamaSwapEnabled:   s.llamaSwap != nil,
		LlamaServiceLabel:  strings.TrimSuffix(s.cfg.LlamaService, ".service"),
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

	if !req.Force {
		// Check each quant's destination directory individually.
		// New nested layout: ModelsDir/basename(repoID)/quantSubdirName(filename)
		parentDir := filepath.Join(s.cfg.ModelsDir, filepath.Base(req.RepoID))
		for _, filename := range req.Filenames {
			quantDir := quantSubdirName(filename)
			if quantDir == "" {
				continue
			}
			destDir := filepath.Join(parentDir, quantDir)
			if _, err := os.Stat(destDir); err == nil {
				existingRepoID := readModelMeta(parentDir).RepoID
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{
					"conflict":       "exists",
					"modelName":      modelNameFromFilename(filename),
					"existingRepoId": existingRepoID,
				})
				return
			}
		}
	}

	if err := s.dl.start(req.RepoID, req.Filenames, req.SidecarFiles, req.TotalBytes, req.Force); err != nil {
		log.Printf("error: start download %s %v: %v", req.RepoID, req.Filenames, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"conflict": "busy", "message": err.Error()})
		return
	}
	log.Printf("download queued: %s %v", req.RepoID, req.Filenames)
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
	log.Printf("restarting service %s", s.cfg.LlamaService)
	if err := restartService(s.cfg.LlamaService); err != nil {
		log.Printf("error: restart %s: %v", s.cfg.LlamaService, err)
		http.Error(w, "failed to restart service: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("service %s restarted", s.cfg.LlamaService)
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
	if err := s.preset.UpsertModelKeys(name, kvs); err != nil {
		http.Error(w, "failed to update preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleGetPresetRaw(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	body, err := s.preset.ReadRaw(name)
	if err != nil {
		http.Error(w, "failed to read preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body))
}

func (s *server) handleUpdatePresetRaw(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	body := strings.TrimRight(string(raw), "\r\n")
	if err := s.preset.WriteRaw(name, body); err != nil {
		http.Error(w, "failed to write preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleRestartSelf(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SelfService == "" {
		http.Error(w, "selfService not configured", http.StatusNotImplemented)
		return
	}
	log.Printf("restarting self service %s", s.cfg.SelfService)
	w.WriteHeader(http.StatusAccepted)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := restartService(s.cfg.SelfService); err != nil {
			log.Printf("error: restart self %s: %v", s.cfg.SelfService, err)
		}
	}()
}

// -- llama-swap per-model raw YAML handlers --

func (s *server) handleGetLlamaSwapRaw(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	body, err := s.llamaSwap.ReadRaw(name)
	if err != nil {
		http.Error(w, "failed to read config.yaml: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body))
}

func (s *server) handlePutLlamaSwapRaw(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.llamaSwap.WriteRaw(name, strings.TrimRight(string(raw), "\r\n")); err != nil {
		http.Error(w, "failed to write config.yaml: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// -- llama-swap command templates handlers --

func (s *server) handleGetLlamaSwapTemplates(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	writeJSON(w, s.llamaSwap.LoadTemplates())
}

func (s *server) handlePutLlamaSwapTemplates(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	if err := s.llamaSwap.UpdateTemplatesFromJSON(io.LimitReader(r.Body, 64<<10)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// -- llama-swap group membership handlers --

func (s *server) handleGetLlamaSwapGroups(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	groups, err := s.llamaSwap.ListGroups(name)
	if err != nil {
		http.Error(w, "failed to read config.yaml: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if groups == nil {
		writeJSON(w, []struct{}{})
		return
	}
	writeJSON(w, groups)
}

func (s *server) handlePutLlamaSwapGroups(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	var groupNames []string
	if err := json.NewDecoder(r.Body).Decode(&groupNames); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.llamaSwap.SetGroupMembership(name, groupNames); err != nil {
		http.Error(w, "failed to write config.yaml: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// -- Full config file handlers (llama-swap config.yaml or models.ini) --

func (s *server) handleGetLlamaSwapConfig(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	body, err := s.llamaSwap.ReadAll()
	if err != nil {
		http.Error(w, "failed to read config.yaml: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body))
}

func (s *server) handlePutLlamaSwapConfig(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.llamaSwap.WriteAll(string(raw)); err != nil {
		http.Error(w, "failed to write config.yaml: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleGetPresetConfig(w http.ResponseWriter, r *http.Request) {
	body, err := s.preset.ReadAll()
	if err != nil {
		http.Error(w, "failed to read models.ini: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body))
}

func (s *server) handlePutPresetConfig(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.preset.WriteAll(string(raw)); err != nil {
		http.Error(w, "failed to write models.ini: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
