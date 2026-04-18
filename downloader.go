package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const metaFilename = ".gguf-manager.json"

type modelMeta struct {
	RepoID string `json:"repoId"`
}

func writeModelMeta(dir, repoID string) error {
	b, _ := json.Marshal(modelMeta{RepoID: repoID})
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

type downloader struct {
	cfg    Config
	preset *presetManager
	mu     sync.Mutex
	active string
	busy   bool
	lines  []string
	cancel context.CancelFunc
}

func newDownloader(cfg Config, pm *presetManager) *downloader {
	return &downloader{cfg: cfg, preset: pm}
}

// modelNameFromFilename strips shard suffixes and .gguf to derive a directory name.
func modelNameFromFilename(filename string) string {
	base := filepath.Base(filename)
	re := regexp.MustCompile(`-\d{5}-of-\d{5}`)
	base = re.ReplaceAllString(base, "")
	return strings.TrimSuffix(base, ".gguf")
}

// shardPattern returns a glob pattern matching all shards of the given file.
func shardPattern(filename string) string {
	base := filepath.Base(filename)
	re := regexp.MustCompile(`-\d{5}-of-\d{5}(\.gguf)$`)
	if re.MatchString(base) {
		stem := re.ReplaceAllString(base, "")
		return stem + "*.gguf"
	}
	return base
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

// start begins a download. If force is true and the model directory already
// exists, the existing directory is renamed to <dir>.old before downloading;
// it is restored on failure and deleted on success.
func (d *downloader) start(repoID, filename string, sidecarFiles []string, force bool) error {
	if strings.Contains(filename, "..") || strings.HasPrefix(filename, "/") {
		return fmt.Errorf("invalid filename")
	}
	for _, sf := range sidecarFiles {
		if strings.Contains(sf, "..") || strings.HasPrefix(sf, "/") {
			return fmt.Errorf("invalid sidecar filename: %s", sf)
		}
	}
	modelName := modelNameFromFilename(filename)
	if modelName == "" || strings.ContainsAny(modelName, "/\\") {
		return fmt.Errorf("could not derive valid model name from filename")
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.busy {
		return fmt.Errorf("download already in progress: %s", d.active)
	}

	destDir := filepath.Join(d.cfg.ModelsDir, modelName)
	oldDir := ""
	if _, err := os.Stat(destDir); err == nil {
		// Model already exists. Rename it out of the way so we can restore it
		// if the new download fails.
		oldDir = destDir + ".old"
		// Remove any stale .old from a previous failed redownload.
		_ = os.RemoveAll(oldDir)
		if err := os.Rename(destDir, oldDir); err != nil {
			return fmt.Errorf("could not move existing model: %w", err)
		}
	}

	pattern := shardPattern(filename)
	label := fmt.Sprintf("%s — %s", repoID, filename)
	switch len(sidecarFiles) {
	case 1:
		label += " + " + sidecarFiles[0]
	case 2, 3:
		label += " + " + strings.Join(sidecarFiles, ", ")
	default:
		if len(sidecarFiles) > 3 {
			label += fmt.Sprintf(" + %d companion files", len(sidecarFiles))
		}
	}
	d.active = label
	d.busy = true
	d.lines = nil

	ctx, cancelFn := context.WithCancel(context.Background())
	d.cancel = cancelFn

	go d.run(ctx, repoID, pattern, sidecarFiles, destDir, modelName, oldDir)
	return nil
}

func (d *downloader) appendLine(line string) {
	d.mu.Lock()
	d.lines = append(d.lines, line)
	d.mu.Unlock()
}

func (d *downloader) run(ctx context.Context, repoID, pattern string, sidecarFiles []string, destDir, modelName, oldDir string) {
	args := []string{"download", repoID, "--include", pattern}
	for _, sf := range sidecarFiles {
		args = append(args, "--include", sf)
	}
	args = append(args, "--local-dir", destDir)

	cmd := exec.CommandContext(ctx, "hf", args...)
	// Send SIGINT on context cancellation; WaitDelay gives the process time to
	// clean up before SIGKILL is sent automatically.
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return cmd.Process.Signal(os.Interrupt)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

	if d.cfg.HFToken != "" {
		cmd.Env = append(os.Environ(), "HF_TOKEN="+d.cfg.HFToken)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		d.restoreOnFailure(oldDir, destDir)
		d.finishWithError(fmt.Errorf("stdout pipe: %w", err))
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		d.restoreOnFailure(oldDir, destDir)
		d.finishWithError(fmt.Errorf("stderr pipe: %w", err))
		return
	}
	if err := cmd.Start(); err != nil {
		d.restoreOnFailure(oldDir, destDir)
		d.finishWithError(fmt.Errorf("failed to start hf: %w", err))
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			d.appendLine(sc.Text())
		}
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			d.appendLine(sc.Text())
		}
	}()
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			d.appendLine("[gguf-manager] download cancelled")
		} else {
			d.restoreOnFailure(oldDir, destDir)
			d.finishWithError(fmt.Errorf("hf download failed: %w", err))
			return
		}
		d.restoreOnFailure(oldDir, destDir)
		d.mu.Lock()
		d.busy = false
		d.cancel = nil
		d.mu.Unlock()
		return
	}

	// Success: write metadata, clean up old dir, update managed.ini.
	if err := writeModelMeta(destDir, repoID); err != nil {
		d.appendLine(fmt.Sprintf("[gguf-manager] warning: could not write metadata: %v", err))
	}
	if oldDir != "" {
		if err := os.RemoveAll(oldDir); err != nil {
			d.appendLine(fmt.Sprintf("[gguf-manager] warning: could not remove old model: %v", err))
		}
	}

	modelPath := filepath.Join(destDir, filepath.Base(pattern))
	if strings.Contains(filepath.Base(pattern), "*") {
		modelPath = destDir
	}
	mmprojPath := ""
	for _, sf := range sidecarFiles {
		if matchesMmproj(sf) {
			mmprojPath = filepath.Join(destDir, filepath.Base(sf))
			break
		}
	}
	if err := d.preset.AddModel(modelName, modelPath, mmprojPath); err != nil {
		d.appendLine(fmt.Sprintf("[gguf-manager] warning: could not update managed.ini: %v", err))
	}

	d.appendLine("[gguf-manager] download complete, restarting service...")
	if err := restartService(d.cfg.LlamaService); err != nil {
		d.appendLine(fmt.Sprintf("[gguf-manager] warning: failed to restart service: %v", err))
	} else {
		d.appendLine("[gguf-manager] service restarted successfully")
	}

	d.mu.Lock()
	d.busy = false
	d.cancel = nil
	d.mu.Unlock()
}

// restoreOnFailure removes any partial destDir and renames oldDir back.
func (d *downloader) restoreOnFailure(oldDir, destDir string) {
	if oldDir == "" {
		return
	}
	_ = os.RemoveAll(destDir)
	if err := os.Rename(oldDir, destDir); err != nil {
		d.appendLine(fmt.Sprintf("[gguf-manager] warning: could not restore old model: %v", err))
	} else {
		d.appendLine("[gguf-manager] restored previous model after failed download")
	}
}

func (d *downloader) finishWithError(err error) {
	d.mu.Lock()
	d.lines = append(d.lines, fmt.Sprintf("[error] %v", err))
	d.busy = false
	d.cancel = nil
	d.mu.Unlock()
}

func (d *downloader) streamSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	d.mu.Lock()
	idle := !d.busy && len(d.lines) == 0
	d.mu.Unlock()

	if idle {
		writeSSEEvent(w, "status", map[string]string{"status": "idle"})
		flusher.Flush()
		return
	}

	sent := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
		}

		d.mu.Lock()
		snapshot := make([]string, len(d.lines))
		copy(snapshot, d.lines)
		busy := d.busy
		d.mu.Unlock()

		for ; sent < len(snapshot); sent++ {
			writeSSEEvent(w, "line", snapshot[sent])
			flusher.Flush()
		}

		if !busy {
			writeSSEEvent(w, "status", map[string]string{"status": "done"})
			flusher.Flush()
			return
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, eventType string, data any) {
	payload, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
}
