package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type localModel struct {
	RepoID        string   `json:"repoId"`
	Path          string   `json:"path"`
	Files         []string `json:"files"`
	SizeBytes     int64    `json:"sizeBytes"`
	LoadedAliases []string `json:"loadedAliases"`
	InConfig      bool     `json:"inConfig"`
	IsLocal       bool     `json:"isLocal"`
	SourceUnknown bool     `json:"sourceUnknown"`
}

type llamaModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func iniModelDir(section map[string]string) string {
	p := section["model"]
	if p == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(p), ".gguf") {
		return filepath.Dir(p)
	}
	return p
}

func (s *Server) HandleLocal(w http.ResponseWriter, r *http.Request) {
	loadedModels, _ := s.fetchLoadedModels()

	type configEntry struct {
		name  string
		paths []string
	}
	var configEntries []configEntry
	appendConfigPath := func(name, p string) {
		if strings.TrimSpace(p) == "" {
			return
		}
		configEntries = append(configEntries, configEntry{name: name, paths: []string{p}})
	}
	if pf, err := s.preset.LoadView(); err == nil {
		for name, sec := range pf.Sections {
			appendConfigPath(name, sec["model"])
			appendConfigPath(name, sec["mmproj"])
			appendConfigPath(name, sec["vae"])
			appendConfigPath(name, sec["clip_l"])
			appendConfigPath(name, sec["clip_g"])
			appendConfigPath(name, sec["t5xxl"])
		}
	}
	if s.llamaSwap != nil {
		if lsModels, err := s.llamaSwap.ListModels(); err == nil {
			for _, m := range lsModels {
				if len(m.ReferencedPaths) > 0 {
					configEntries = append(configEntries, configEntry{name: m.Name, paths: m.ReferencedPaths})
				} else if m.ModelPath != "" {
					configEntries = append(configEntries, configEntry{name: m.Name, paths: []string{m.ModelPath}})
				}
			}
		}
	}

	inConfigFor := func(repoDir string) bool {
		rd := filepath.Clean(repoDir)
		sep := string(filepath.Separator)
		for _, e := range configEntries {
			for _, cp := range e.paths {
				p := filepath.Clean(cp)
				if p == rd || strings.HasPrefix(p, rd+sep) {
					return true
				}
			}
		}
		return false
	}

	loadedAliasesFor := func(repoDir string) []string {
		rd := filepath.Clean(repoDir)
		sep := string(filepath.Separator)
		var aliases []string
		seen := make(map[string]struct{})
		for _, e := range configEntries {
			if _, ok := loadedModels[e.name]; !ok {
				continue
			}
			if _, ok := seen[e.name]; ok {
				continue
			}
			for _, cp := range e.paths {
				p := filepath.Clean(cp)
				if p == rd || strings.HasPrefix(p, rd+sep) {
					aliases = append(aliases, e.name)
					seen[e.name] = struct{}{}
					break
				}
			}
		}
		if aliases == nil {
			return []string{}
		}
		return aliases
	}

	ignorePatterns := s.cfg.RootIgnorePatterns
	if s.deps.EffectiveRootIgnorePattern != nil {
		ignorePatterns = s.deps.EffectiveRootIgnorePattern(s.cfg)
	}

	models := make([]localModel, 0)
	entries, _ := os.ReadDir(s.cfg.ModelsDir)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if s.deps.IsIgnoredEntry != nil && s.deps.IsIgnoredEntry(entry.Name(), ignorePatterns, s.cfg.ShowDotFiles, entry.IsDir()) {
			continue
		}
		dirPath := filepath.Join(s.cfg.ModelsDir, entry.Name())

		if s.deps.IsOrgDir != nil && s.deps.IsOrgDir(dirPath) {
			repoEntries, _ := os.ReadDir(dirPath)
			for _, repoEntry := range repoEntries {
				if !repoEntry.IsDir() {
					continue
				}
				repoDir := filepath.Join(dirPath, repoEntry.Name())
				meta := s.deps.ReadModelMeta(repoDir)
				repoID := meta.RepoID
				if repoID == "" {
					repoID = entry.Name() + "/" + repoEntry.Name()
				}
				files, size := s.deps.ScanFilesRelative(repoDir)
				if len(files) == 0 {
					continue
				}
				models = append(models, localModel{
					RepoID:        repoID,
					Path:          repoDir,
					Files:         files,
					SizeBytes:     size,
					LoadedAliases: loadedAliasesFor(repoDir),
					InConfig:      inConfigFor(repoDir),
					IsLocal:       meta.SkipHFSync,
					SourceUnknown: false,
				})
			}
		} else {
			meta := s.deps.ReadModelMeta(dirPath)
			repoID := meta.RepoID
			sourceUnknown := false

			files, size := s.deps.ScanFilesRelative(dirPath)
			if len(files) == 0 {
				continue
			}
			if repoID == "" && !meta.SkipHFSync {
				var ggufFiles []string
				for _, f := range files {
					b := filepath.Base(f)
					if strings.HasSuffix(b, ".gguf") && !s.deps.MatchesMmproj(b) {
						ggufFiles = append(ggufFiles, f)
					}
				}
				if len(ggufFiles) > 0 {
					repoID = s.deps.DetectRepoIDFromGGUF(dirPath, ggufFiles)
					if repoID != "" {
						_ = s.deps.WriteModelMeta(dirPath, ModelMeta{RepoID: repoID})
					} else {
						sourceUnknown = true
					}
				}
			}

			models = append(models, localModel{
				RepoID:        repoID,
				Path:          dirPath,
				Files:         files,
				SizeBytes:     size,
				LoadedAliases: loadedAliasesFor(dirPath),
				InConfig:      inConfigFor(dirPath),
				IsLocal:       meta.SkipHFSync,
				SourceUnknown: sourceUnknown,
			})
		}
	}

	writeJSON(w, models)
}

func (s *Server) fetchLoadedModels() (map[string]struct{}, error) {
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

func (s *Server) cleanupEmptyParentDir(parentDir string) {
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
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".gguf") && !s.deps.MatchesMmproj(f.Name()) {
				return
			}
		}
	}
	if err := s.deps.RemoveAllWritable(parentDir); err != nil {
		log.Printf("warning: could not remove empty model parent dir %q: %v", parentDir, err)
	}
}

func (s *Server) HandleDeleteLocal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}

	modelDir := ""
	fromINI := false
	if pf, err := s.preset.LoadView(); err == nil {
		if sec, ok := pf.Sections[name]; ok {
			modelDir = iniModelDir(sec)
			fromINI = true
		}
	}
	if modelDir == "" {
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
	} else {
		if err := s.deps.RemoveAllWritable(modelDir); err != nil {
			log.Printf("error: delete model %q: %v", name, err)
			http.Error(w, "failed to delete: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("deleted model %q", name)
		s.cleanupEmptyParentDir(filepath.Dir(modelDir))
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
		if err := s.deps.RestartService(s.cfg.LlamaService); err != nil {
			log.Printf("warning: failed to restart %s: %v", s.cfg.LlamaService, err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
