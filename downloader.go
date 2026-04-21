package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

const metaFilename = ".w84ggufman.json"

type modelMeta struct {
	RepoID     string   `json:"repoId"`
	SkipHFSync bool     `json:"skip_hf_sync,omitempty"`
	Ignore     []string `json:"ignore,omitempty"` // per-dir ignore patterns (replaces server defaults)
}

func writeModelMeta(dir string, meta modelMeta) error {
	b, _ := json.Marshal(meta)
	return os.WriteFile(filepath.Join(dir, metaFilename), b, 0664)
}

func readModelMeta(dir string) modelMeta {
	data, err := os.ReadFile(filepath.Join(dir, metaFilename))
	if err != nil {
		return modelMeta{}
	}
	var m modelMeta
	_ = json.Unmarshal(data, &m)
	return m
}

type progressInfo struct {
	Pct     int   `json:"pct"`     // 0–100; -1 when total is unknown
	Speed   int64 `json:"speed"`   // bytes/sec
	ETA     int   `json:"eta"`     // seconds remaining; -1 when unknown
	DLBytes int64 `json:"dlBytes"` // bytes downloaded so far
}

// downloadJob describes a single file's download within a batch.
type downloadJob struct {
	filename string // HF filename (may include subdir prefix like "Q4_K_M/model.gguf")
	pattern  string // shardPattern(filename): glob for sharded, literal for single
}

type downloader struct {
	cfg        Config
	preset     *presetManager
	llamaSwap  *llamaSwapManager
	mu         sync.Mutex
	active     string
	busy       bool
	lines      []string
	cancel     context.CancelFunc
	totalBytes int64
	progress   *progressInfo
}

func newDownloader(cfg Config, pm *presetManager, lsm *llamaSwapManager) *downloader {
	return &downloader{cfg: cfg, preset: pm, llamaSwap: lsm}
}

func (d *downloader) activeInfo() (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active, d.busy
}

func (d *downloader) cancelDownload() {
	d.mu.Lock()
	fn := d.cancel
	d.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (d *downloader) appendLine(line string) {
	d.mu.Lock()
	d.lines = append(d.lines, line)
	d.mu.Unlock()
}

func (d *downloader) finishWithError(err error) {
	log.Printf("download error: %v", err)
	d.mu.Lock()
	d.lines = append(d.lines, fmt.Sprintf("[error] %v", err))
	d.busy = false
	d.cancel = nil
	d.progress = nil
	d.mu.Unlock()
}
