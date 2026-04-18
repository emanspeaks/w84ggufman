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
	"syscall"
	"time"
)

type downloader struct {
	cfg    Config
	mu     sync.Mutex
	active string
	busy   bool
	lines  []string
	cancel context.CancelFunc
}

func newDownloader(cfg Config) *downloader {
	return &downloader{cfg: cfg}
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

func (d *downloader) start(repoID, filename string) error {
	if strings.Contains(filename, "..") || strings.HasPrefix(filename, "/") {
		return fmt.Errorf("invalid filename")
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

	pattern := shardPattern(filename)
	destDir := filepath.Join(d.cfg.ModelsDir, modelName)
	d.active = fmt.Sprintf("%s — %s", repoID, filename)
	d.busy = true
	d.lines = nil

	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	go d.run(ctx, repoID, pattern, destDir)
	return nil
}

func (d *downloader) cancelDownload() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.busy || d.cancel == nil {
		return false
	}
	d.cancel()
	return true
}

func (d *downloader) appendLine(line string) {
	d.mu.Lock()
	d.lines = append(d.lines, line)
	d.mu.Unlock()
}

func (d *downloader) run(ctx context.Context, repoID, pattern, destDir string) {
	args := []string{"download", repoID, "--include", pattern, "--local-dir", destDir}
	cmd := exec.CommandContext(ctx, "hf", args...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second
	if d.cfg.HFToken != "" {
		cmd.Env = append(os.Environ(), "HF_TOKEN="+d.cfg.HFToken)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		d.finishWithError(fmt.Errorf("stdout pipe: %w", err))
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		d.finishWithError(fmt.Errorf("stderr pipe: %w", err))
		return
	}
	if err := cmd.Start(); err != nil {
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
			d.mu.Lock()
			d.busy = false
			d.mu.Unlock()
			return
		}
		d.finishWithError(fmt.Errorf("hf download failed: %w", err))
		return
	}

	d.appendLine("[gguf-manager] download complete, restarting service...")
	if err := restartService(d.cfg.LlamaService); err != nil {
		d.appendLine(fmt.Sprintf("[gguf-manager] warning: failed to restart service: %v", err))
	} else {
		d.appendLine("[gguf-manager] service restarted successfully")
	}

	d.mu.Lock()
	d.busy = false
	d.mu.Unlock()
}

func (d *downloader) finishWithError(err error) {
	d.mu.Lock()
	d.lines = append(d.lines, fmt.Sprintf("[error] %v", err))
	d.busy = false
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
