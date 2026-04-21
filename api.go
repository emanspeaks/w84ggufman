package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
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

// localModel represents one HF repo (or local folder) as a single card.
// Files contains relative paths from Path for all files in the repo directory.
type localModel struct {
	RepoID        string   `json:"repoId"` // "org/repo", or "" for unknown
	Path          string   `json:"path"`   // absolute dir path on disk
	Files         []string `json:"files"`  // relative paths of all files under Path
	SizeBytes     int64    `json:"sizeBytes"`
	LoadedAliases []string `json:"loadedAliases"` // model aliases loaded from this repo
	InConfig      bool     `json:"inConfig"`      // any file in this repo is referenced in config files
	IsLocal       bool     `json:"isLocal"`       // true = not an HF repo
	SourceUnknown bool     `json:"sourceUnknown"` // couldn't determine HF source
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

// defaultIgnorePatterns are the patterns applied to top-level modelsDir entries
// unless overridden by a .w84ggufman.json at the modelsDir root.
var defaultIgnorePatterns = []string{".cache", ".w84ggufman*"}

// matchesIgnorePattern reports whether name (a single path component) matches a
// preprocessed gitignore-style pattern. isDir indicates whether name refers to a
// directory; trailing-slash and "/**" patterns only match directories. The caller
// must have already stripped leading "!" negation and backslash escapes.
func matchesIgnorePattern(name, pattern string, isDir bool) bool {
	// Trailing "/" → directory-only match.
	if strings.HasSuffix(pattern, "/") {
		if !isDir {
			return false
		}
		pattern = strings.TrimSuffix(pattern, "/")
	}

	// Strip a leading "/" (anchors to .gitignore root; irrelevant for name-level matching).
	pattern = strings.TrimPrefix(pattern, "/")

	// "**/name" → match at any depth; strip the prefix.
	if strings.HasPrefix(pattern, "**/") {
		pattern = strings.TrimPrefix(pattern, "**/")
	}

	// "name/**" → matches everything inside name; treat as dir-only match on name.
	if strings.HasSuffix(pattern, "/**") {
		if !isDir {
			return false
		}
		pattern = strings.TrimSuffix(pattern, "/**")
	}

	// Collapse any remaining "**" → "*" (no path separators in a single name).
	pattern = strings.ReplaceAll(pattern, "**", "*")

	matched, _ := filepath.Match(pattern, name)
	return matched
}

// isIgnoredEntry reports whether an entry should be excluded. Patterns are
// evaluated in gitignore order: later patterns override earlier ones. Blank
// lines and "#"-prefixed lines are comments; "\#" and "\!" escape the
// respective special chars. "!" negates (whitelists) a previous match.
// Trailing unescaped whitespace is trimmed. The dot-files default acts as
// the implicit first rule and can be overridden by a later "!.name" pattern.
func isIgnoredEntry(name string, patterns []string, showDotFiles, isDir bool) bool {
	ignored := !showDotFiles && strings.HasPrefix(name, ".")
	for _, raw := range patterns {
		// Trim trailing unescaped whitespace.
		p := strings.TrimRight(raw, " \t")
		if len(p) < len(raw) && strings.HasSuffix(p, "\\") {
			p = p + " " // backslash-escaped trailing space is significant
		}
		// Skip blank lines and comments.
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		// Check for negation or leading backslash escape.
		negate := false
		if strings.HasPrefix(p, "!") {
			negate = true
			p = p[1:]
		} else if strings.HasPrefix(p, "\\") {
			p = p[1:] // \# or \! → literal character
		}
		if p == "" {
			continue
		}
		if matchesIgnorePattern(name, p, isDir) {
			if negate {
				ignored = false
			} else {
				ignored = true
			}
		}
	}
	return ignored
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
		if info.Name() == metaFilename {
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

func (s *server) handleLocal(w http.ResponseWriter, r *http.Request) {
	loadedModels, _ := s.fetchLoadedModels()

	// Collect (name, modelPath) pairs from config files for InConfig / LoadedAliases.
	type configEntry struct {
		name      string
		modelPath string
	}
	var configEntries []configEntry
	if pf, err := s.preset.Load(); err == nil {
		for name, sec := range pf.Sections {
			if p := sec["model"]; p != "" {
				configEntries = append(configEntries, configEntry{name, p})
			}
		}
	}
	if s.llamaSwap != nil {
		if lsModels, err := s.llamaSwap.ListModels(); err == nil {
			for _, m := range lsModels {
				if m.ModelPath != "" {
					configEntries = append(configEntries, configEntry{m.Name, m.ModelPath})
				}
			}
		}
	}

	// inConfigFor returns true if any config-registered path is inside repoDir.
	inConfigFor := func(repoDir string) bool {
		rd := filepath.Clean(repoDir)
		sep := string(filepath.Separator)
		for _, e := range configEntries {
			p := filepath.Clean(e.modelPath)
			if p == rd || strings.HasPrefix(p, rd+sep) {
				return true
			}
		}
		return false
	}

	// loadedAliasesFor returns names of currently-loaded models whose paths
	// are inside repoDir.
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
			p := filepath.Clean(e.modelPath)
			if p == rd || strings.HasPrefix(p, rd+sep) {
				aliases = append(aliases, e.name)
				seen[e.name] = struct{}{}
			}
		}
		if aliases == nil {
			return []string{}
		}
		return aliases
	}

	// Determine active ignore patterns: check for .w84ggufman.json at modelsDir root.
	rootMeta := readModelMeta(s.cfg.ModelsDir)
	ignorePatterns := rootMeta.Ignore
	if len(ignorePatterns) == 0 {
		ignorePatterns = defaultIgnorePatterns
	}

	models := make([]localModel, 0)
	entries, _ := os.ReadDir(s.cfg.ModelsDir)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if isIgnoredEntry(entry.Name(), ignorePatterns, s.cfg.ShowDotFiles, entry.IsDir()) {
			continue
		}
		dirPath := filepath.Join(s.cfg.ModelsDir, entry.Name())

		if isOrgDir(dirPath) {
			// org/repo layout: each subdir of the org dir is a repo.
			repoEntries, _ := os.ReadDir(dirPath)
			for _, repoEntry := range repoEntries {
				if !repoEntry.IsDir() {
					continue
				}
				repoDir := filepath.Join(dirPath, repoEntry.Name())
				meta := readModelMeta(repoDir)
				repoID := meta.RepoID
				if repoID == "" {
					repoID = entry.Name() + "/" + repoEntry.Name()
				}
				files, size := scanFilesRelative(repoDir)
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
			// Old or local layout: one card per immediate child of modelsDir.
			meta := readModelMeta(dirPath)
			repoID := meta.RepoID
			sourceUnknown := false

			files, size := scanFilesRelative(dirPath)
			if len(files) == 0 {
				continue
			}
			if repoID == "" && !meta.SkipHFSync {
				var ggufFiles []string
				for _, f := range files {
					b := filepath.Base(f)
					if strings.HasSuffix(b, ".gguf") && !matchesMmproj(b) {
						ggufFiles = append(ggufFiles, f)
					}
				}
				if len(ggufFiles) > 0 {
					repoID = detectRepoIDFromGGUF(dirPath, ggufFiles)
					if repoID != "" {
						_ = writeModelMeta(dirPath, modelMeta{RepoID: repoID})
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
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(s.cfg.HFToken))
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
	repoInfo, err := fetchRepoInfo(repoID, s.cfg.HFToken)
	if err != nil {
		http.Error(w, "failed to fetch repo: "+err.Error(), http.StatusBadGateway)
		return
	}
	repoDir := filepath.Join(s.cfg.ModelsDir, filepath.FromSlash(repoID))
	localFiles, _ := scanFilesRelative(repoDir)
	repoInfo.PresentFiles, repoInfo.RogueFiles = matchLocalToHF(localFiles, repoInfo)
	writeJSON(w, repoInfo)
}

// matchLocalToHF matches local files to HF files by basename (handling path
// mismatches from old layouts), returning the set of local relative paths that
// matched (presentFiles) and those that didn't (rogueFiles).
func matchLocalToHF(localFiles []string, info *HFRepoInfo) (present []string, rogue []string) {
	// Build basename → true from all HF files (models + sidecars).
	hfBasenames := make(map[string]struct{})
	for _, f := range info.Models {
		hfBasenames[filepath.Base(f.Filename)] = struct{}{}
	}
	for _, f := range info.Sidecars {
		hfBasenames[filepath.Base(f.Filename)] = struct{}{}
	}

	for _, lf := range localFiles {
		base := filepath.Base(lf)
		if _, ok := hfBasenames[base]; ok {
			present = append(present, lf)
			continue
		}
		// Shard fallback: strip shard digits and check if stem matches any HF file.
		stemmed := shardRe.ReplaceAllString(base, ".gguf")
		stemBase := strings.TrimSuffix(stemmed, ".gguf")
		matched := false
		if stemBase != "" && stemBase != strings.TrimSuffix(base, ".gguf") {
			for k := range hfBasenames {
				if strings.TrimSuffix(shardRe.ReplaceAllString(k, ".gguf"), ".gguf") == stemBase {
					matched = true
					break
				}
			}
		}
		if matched {
			present = append(present, lf)
		} else {
			rogue = append(rogue, lf)
		}
	}
	return
}

func (s *server) handleLocalFiles(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" || strings.Contains(id, "..") {
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
	files, _ := scanFilesRelative(repoDir)
	writeJSON(w, &HFRepoInfo{
		LocalOnly:  true,
		RogueFiles: files,
	})
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

func (s *server) handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
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
	if err := removeAllWritable(repoDir); err != nil {
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

// findFileByBasename walks dir recursively and returns the first file whose
// base name equals target, or "" if not found.
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

func (s *server) handleDeleteFiles(w http.ResponseWriter, r *http.Request) {
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
		// If exact path not found, search recursively by basename (handles files
		// that were misplaced by the old-layout migration).
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			base := filepath.Base(fullPath)
			fullPath = findFileByBasename(repoDir, base)
		}
		if fullPath == "" {
			continue // not found anywhere — silently skip
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

// // -- llama-swap group membership handlers --

// func (s *server) handleGetLlamaSwapGroups(w http.ResponseWriter, r *http.Request) {
// 	if s.llamaSwap == nil {
// 		http.Error(w, "llama-swap not configured", http.StatusNotFound)
// 		return
// 	}
// 	name := r.PathValue("name")
// 	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
// 		http.Error(w, "invalid model name", http.StatusBadRequest)
// 		return
// 	}
// 	groups, err := s.llamaSwap.ListGroups(name)
// 	if err != nil {
// 		http.Error(w, "failed to read config.yaml: "+err.Error(), http.StatusInternalServerError)
// 		return
// 	}
// 	if groups == nil {
// 		writeJSON(w, []struct{}{})
// 		return
// 	}
// 	writeJSON(w, groups)
// }

// func (s *server) handlePutLlamaSwapGroups(w http.ResponseWriter, r *http.Request) {
// 	if s.llamaSwap == nil {
// 		http.Error(w, "llama-swap not configured", http.StatusNotFound)
// 		return
// 	}
// 	name := r.PathValue("name")
// 	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
// 		http.Error(w, "invalid model name", http.StatusBadRequest)
// 		return
// 	}
// 	var groupNames []string
// 	if err := json.NewDecoder(r.Body).Decode(&groupNames); err != nil {
// 		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
// 		return
// 	}
// 	if err := s.llamaSwap.SetGroupMembership(name, groupNames); err != nil {
// 		http.Error(w, "failed to write config.yaml: "+err.Error(), http.StatusInternalServerError)
// 		return
// 	}
// 	w.WriteHeader(http.StatusNoContent)
// }

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
