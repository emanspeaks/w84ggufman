package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const hfCacheFilename = ".w84cache"

type hfCacheEntry struct {
	DownloadedSha string    `json:"downloadedSha"`
	LatestSha     string    `json:"latestSha"`
	CheckedAt     time.Time `json:"checkedAt"`
}

func readHFCache(dir string) hfCacheEntry {
	data, err := os.ReadFile(filepath.Join(dir, hfCacheFilename))
	if err != nil {
		return hfCacheEntry{}
	}
	var e hfCacheEntry
	_ = json.Unmarshal(data, &e)
	return e
}

func writeHFCache(dir string, e hfCacheEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, hfCacheFilename), b, 0664)
}

// oldestDownloadedSha reads HF CLI download metadata for the given (already
// filtered) relative file list and returns the repo commit sha from the file
// with the oldest download timestamp. Starting from our own file list (not the
// cache dir) ensures we ignore metadata for files we've since deleted or moved.
func oldestDownloadedSha(repoDir string, files []string) string {
	cacheBase := filepath.Join(repoDir, ".cache", "huggingface", "download")
	var oldestSha string
	var oldestTime float64 = -1
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(cacheBase, filepath.FromSlash(f)+".metadata"))
		if err != nil {
			continue
		}
		lines := strings.SplitN(strings.TrimRight(string(data), "\r\n"), "\n", 3)
		if len(lines) < 3 {
			continue
		}
		sha := strings.TrimSpace(lines[0])
		ts, err := strconv.ParseFloat(strings.TrimSpace(lines[2]), 64)
		if err != nil || sha == "" {
			continue
		}
		if oldestTime < 0 || ts < oldestTime {
			oldestTime = ts
			oldestSha = sha
		}
	}
	return oldestSha
}

type updateChecker struct {
	cfg    Config
	mu     sync.RWMutex
	counts map[string]bool // abs repoDir -> hasUpdate
}

func newUpdateChecker(cfg Config) *updateChecker {
	uc := &updateChecker{
		cfg:    cfg,
		counts: make(map[string]bool),
	}
	go uc.run()
	return uc
}

func (uc *updateChecker) HasUpdateAvailable(dir string) bool {
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	return uc.counts[filepath.Clean(dir)]
}

func (uc *updateChecker) PendingUpdateCount() int {
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	n := 0
	for _, v := range uc.counts {
		if v {
			n++
		}
	}
	return n
}

// recordDownload is called after a successful download. It fetches the current
// HF sha and writes it to .w84cache so future checks use this as the baseline.
func (uc *updateChecker) recordDownload(repoID, repoDir string) {
	sha, err := fetchLatestSha(repoID, uc.cfg.HFToken)
	if err != nil || sha == "" {
		log.Printf("update-check: could not fetch sha for %s: %v", repoID, err)
		return
	}
	e := readHFCache(repoDir)
	e.DownloadedSha = sha
	e.LatestSha = sha
	e.CheckedAt = time.Now()
	if err := writeHFCache(repoDir, e); err != nil {
		log.Printf("update-check: write cache %s: %v", repoDir, err)
	}
	uc.mu.Lock()
	uc.counts[filepath.Clean(repoDir)] = false
	uc.mu.Unlock()
}

func (uc *updateChecker) run() {
	time.Sleep(2 * time.Minute)
	uc.checkAll()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		uc.checkAll()
	}
}

func (uc *updateChecker) checkAll() {
	log.Printf("update-check: scanning %s", uc.cfg.ModelsDir)
	entries, err := os.ReadDir(uc.cfg.ModelsDir)
	if err != nil {
		log.Printf("update-check: readdir %s: %v", uc.cfg.ModelsDir, err)
		return
	}
	rootPatterns := effectiveRootIgnorePatterns(uc.cfg)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Honour root-level ignore rules before touching anything.
		if isIgnoredEntry(entry.Name(), rootPatterns, uc.cfg.ShowDotFiles, true) {
			continue
		}
		dirPath := filepath.Join(uc.cfg.ModelsDir, entry.Name())
		if isOrgDir(dirPath) {
			subs, _ := os.ReadDir(dirPath)
			for _, sub := range subs {
				if sub.IsDir() {
					subPath := filepath.Join(dirPath, sub.Name())
					if !uc.pruneIfEmpty(subPath) {
						uc.checkRepoDir(subPath)
					}
				}
			}
			// Prune org dir itself if it's now empty.
			if remaining, _ := os.ReadDir(dirPath); len(remaining) == 0 {
				if err := os.Remove(dirPath); err != nil {
					log.Printf("update-check: remove empty org dir %q: %v", dirPath, err)
				} else {
					log.Printf("update-check: removed empty org dir %q", dirPath)
				}
			}
		} else {
			if !uc.pruneIfEmpty(dirPath) {
				uc.checkRepoDir(dirPath)
			}
		}
	}
	log.Printf("update-check: done, %d repo(s) with updates available", uc.PendingUpdateCount())
}

// pruneIfEmpty removes a repo dir when it contains no real files after
// applying ignore rules. Returns true if the dir was removed.
func (uc *updateChecker) pruneIfEmpty(dir string) bool {
	files, _ := scanFilesRelative(dir)
	files = filterIgnoredRelativeFiles(dir, files, uc.cfg)
	if len(files) > 0 {
		return false
	}
	if err := removeAllWritable(dir); err != nil {
		log.Printf("update-check: remove empty repo dir %q: %v", dir, err)
		return false
	}
	log.Printf("update-check: removed empty repo dir %q", dir)
	return true
}

func (uc *updateChecker) checkRepoDir(dir string) {
	cleanDir := filepath.Clean(dir)
	e := readHFCache(dir)

	if time.Since(e.CheckedAt) < 23*time.Hour {
		// Checked recently — just refresh in-memory count from on-disk state.
		uc.mu.Lock()
		uc.counts[cleanDir] = e.LatestSha != "" && e.DownloadedSha != "" && e.LatestSha != e.DownloadedSha
		uc.mu.Unlock()
		return
	}

	meta := readModelMeta(dir)
	if meta.SkipHFSync {
		return
	}

	repoID := meta.RepoID
	if repoID == "" {
		// Infer from directory path: <modelsDir>/author/model → "author/model".
		if rel, err := filepath.Rel(uc.cfg.ModelsDir, cleanDir); err == nil {
			rel = filepath.ToSlash(rel)
			if strings.Count(rel, "/") == 1 {
				repoID = rel
			}
		}
	}
	if repoID == "" {
		// Final fallback: read repoID from GGUF file metadata.
		entries, _ := os.ReadDir(dir)
		var files []string
		for _, ent := range entries {
			if !ent.IsDir() {
				files = append(files, ent.Name())
			}
		}
		repoID = detectRepoIDFromGGUF(dir, files)
	}
	if repoID == "" {
		return
	}

	// Always re-derive downloadedSha from HF CLI metadata on each full check.
	// .w84cache is only trusted for the 23h short-circuit above.
	files, _ := scanFilesRelative(dir)
	files = filterIgnoredRelativeFiles(dir, files, uc.cfg)
	e.DownloadedSha = oldestDownloadedSha(dir, files)

	sha, err := fetchLatestSha(repoID, uc.cfg.HFToken)
	if err != nil {
		log.Printf("update-check: %s: %v", repoID, err)
		return
	}

	if e.DownloadedSha == "" {
		// No HF CLI metadata and no prior record: use current sha as baseline.
		e.DownloadedSha = sha
	}
	e.LatestSha = sha
	e.CheckedAt = time.Now()
	if err := writeHFCache(dir, e); err != nil {
		log.Printf("update-check: write cache %s: %v", dir, err)
	}

	hasUpdate := e.LatestSha != e.DownloadedSha
	uc.mu.Lock()
	uc.counts[cleanDir] = hasUpdate
	uc.mu.Unlock()
	if hasUpdate {
		log.Printf("update-check: update available for %s", repoID)
	}
}
