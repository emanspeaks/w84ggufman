package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
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
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(uc.cfg.ModelsDir, entry.Name())
		if isOrgDir(dirPath) {
			subs, _ := os.ReadDir(dirPath)
			for _, sub := range subs {
				if sub.IsDir() {
					uc.checkRepoDir(filepath.Join(dirPath, sub.Name()))
				}
			}
		} else {
			uc.checkRepoDir(dirPath)
		}
	}
	log.Printf("update-check: done, %d repo(s) with updates available", uc.PendingUpdateCount())
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
	if meta.RepoID == "" || meta.SkipHFSync {
		return
	}

	sha, err := fetchLatestSha(meta.RepoID, uc.cfg.HFToken)
	if err != nil {
		log.Printf("update-check: %s: %v", meta.RepoID, err)
		return
	}

	if e.DownloadedSha == "" {
		// First time checking this repo: establish baseline, no update yet.
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
		log.Printf("update-check: update available for %s", meta.RepoID)
	}
}
