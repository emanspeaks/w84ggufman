package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

const metaFilename = ".w84ggufman.json"
const metaFilenameYAML = ".w84ggufman.yaml"

type modelMeta struct {
	RepoID     string   `json:"repoId"             yaml:"repoId"`
	SkipHFSync bool     `json:"skip_hf_sync,omitempty" yaml:"skip_hf_sync,omitempty"`
	Ignore     []string `json:"ignore,omitempty"   yaml:"ignore,omitempty"`
	CtxSize    int      `json:"ctx_size,omitempty" yaml:"ctx_size,omitempty"`
}

func writeModelMeta(dir string, meta modelMeta) error {
	b, _ := json.Marshal(meta)
	return os.WriteFile(filepath.Join(dir, metaFilename), b, 0664)
}

func readModelMeta(dir string) modelMeta {
	// Prefer YAML when present.
	if data, err := os.ReadFile(filepath.Join(dir, metaFilenameYAML)); err == nil {
		var m modelMeta
		if yaml.Unmarshal(data, &m) == nil {
			return m
		}
	}
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

// queueEntry holds a pending download request.
type queueEntry struct {
	id           int64
	repoID       string
	filenames    []string
	sidecarFiles []string
	totalBytes   int64
	label        string
}

type downloader struct {
	cfg             Config
	preset          *presetManager
	llamaSwap       *llamaSwapManager
	mu              sync.Mutex
	active          string
	activeRepoID    string   // repoID of the currently-running download (for dedup)
	activeFilenames []string // filenames of the currently-running download (for dedup)
	busy            bool
	lines           []string
	cancel          context.CancelFunc
	totalBytes      int64
	progress        *progressInfo
	queue           []queueEntry // pending downloads
	queueVer        int64        // incremented on any queue mutation; used by SSE for change detection
	nextID          int64        // monotonic counter for queue entry IDs
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

func (d *downloader) queueEntries() []queueEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]queueEntry, len(d.queue))
	copy(out, d.queue)
	return out
}

func (d *downloader) removeFromQueue(id int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, e := range d.queue {
		if e.id == id {
			d.queue = append(d.queue[:i], d.queue[i+1:]...)
			d.queueVer++
			return true
		}
	}
	return false
}

func (d *downloader) finishWithError(err error) {
	log.Printf("download error: %v", err)
	d.mu.Lock()
	d.lines = append(d.lines, fmt.Sprintf("[error] %v", err))
	d.busy = false
	d.cancel = nil
	d.progress = nil
	d.queue = nil
	d.queueVer++
	d.activeRepoID = ""
	d.activeFilenames = nil
	d.mu.Unlock()
}
