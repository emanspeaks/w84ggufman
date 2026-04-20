package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const metaFilename = ".w84ggufman.json"

// ansiRe strips ANSI/VT100 escape sequences from terminal output.
var ansiRe = regexp.MustCompile(`\x1b(?:\[[0-9;]*[a-zA-Z]|[()][0-9A-Za-z]?)`)

// scanCRLF is a bufio.SplitFunc that treats \r, \n, and \r\n as line endings
// so tqdm progress updates (which use \r) arrive as individual log lines.
func scanCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i := 0; i < len(data); i++ {
		if data[i] == '\r' || data[i] == '\n' {
			j := i + 1
			if data[i] == '\r' && j < len(data) && data[j] == '\n' {
				j++
			}
			return j, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

type modelMeta struct {
	RepoID     string `json:"repoId"`
	SkipHFSync bool   `json:"skip_hf_sync,omitempty"`
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

// removeAllWritable chmod-walks path to make every entry owner-writable before
// calling os.RemoveAll. This is necessary because hf download writes files and
// dirs with restrictive permissions (e.g. 0555 dirs, 0444 files) that would
// cause os.RemoveAll to fail with "permission denied" even when the process
// owns the tree.
func removeAllWritable(path string) error {
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			os.Chmod(p, 0755)
		} else {
			os.Chmod(p, 0644)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// dirSize returns the total size of all regular files under path.
func dirSize(path string) int64 {
	var total int64
	filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func newDownloader(cfg Config, pm *presetManager, lsm *llamaSwapManager) *downloader {
	return &downloader{cfg: cfg, preset: pm, llamaSwap: lsm}
}

// modelNameFromFilename strips shard suffixes and .gguf to derive a directory name.
func modelNameFromFilename(filename string) string {
	base := filepath.Base(filename)
	re := regexp.MustCompile(`-\d{5}-of-\d{5}`)
	base = re.ReplaceAllString(base, "")
	return strings.TrimSuffix(base, ".gguf")
}

// quantSubdirName returns the short quant identifier used as the on-disk subdir name.
// For subdirectory-organized repos (e.g. "Q4_K_M/model.gguf") it returns the innermost
// dir component ("Q4_K_M"). For flat repos it extracts the quant suffix via quantSuffixRe
// (e.g. "Q4_K_M" from "Llama-3-8B-Q4_K_M.gguf"), falling back to the full stem.
// func quantSubdirName(filename string) string {
// 	if idx := strings.LastIndex(filename, "/"); idx >= 0 {
// 		dir := filename[:idx]
// 		if jdx := strings.LastIndex(dir, "/"); jdx >= 0 {
// 			dir = dir[jdx+1:]
// 		}
// 		return dir
// 	}
// 	base := shardRe.ReplaceAllString(filepath.Base(filename), "")
// 	base = strings.TrimSuffix(base, ".gguf")
// 	if m := quantSuffixRe.FindStringSubmatch(base); m != nil {
// 		return m[1]
// 	}
// 	return base
// }

// shardPattern returns a glob pattern matching all shards of the given file.
// For files in subdirectories (subdir-grouped quants) the directory prefix is
// preserved so the hf CLI receives the correct path (e.g. "Q8_0/Model*.gguf").
func shardPattern(filename string) string {
	dir := ""
	base := filename
	if idx := strings.LastIndex(filename, "/"); idx >= 0 {
		dir = filename[:idx+1]
		base = filename[idx+1:]
	}
	re := regexp.MustCompile(`-\d{5}-of-\d{5}(\.gguf)$`)
	if re.MatchString(base) {
		stem := re.ReplaceAllString(base, "")
		return dir + stem + "*.gguf"
	}
	return dir + base
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

// start begins a download. All selected files and sidecars are placed under
// modelsDir/org/repo/, preserving the HF subpath structure within that dir.
func (d *downloader) start(repoID string, filenames []string, sidecarFiles []string, totalBytes int64, _ bool) error {
	if len(filenames) == 0 {
		return fmt.Errorf("at least one filename is required")
	}
	for _, filename := range filenames {
		if strings.Contains(filename, "..") || strings.HasPrefix(filename, "/") {
			return fmt.Errorf("invalid filename: %s", filename)
		}
	}
	for _, sf := range sidecarFiles {
		if strings.Contains(sf, "..") || strings.HasPrefix(sf, "/") {
			return fmt.Errorf("invalid sidecar filename: %s", sf)
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.busy {
		return fmt.Errorf("download already in progress: %s", d.active)
	}

	// All files go to modelsDir/org/repo/ (preserves HF subpath structure).
	repoDir := filepath.Join(d.cfg.ModelsDir, filepath.FromSlash(repoID))

	jobs := make([]downloadJob, 0, len(filenames))
	for _, filename := range filenames {
		jobs = append(jobs, downloadJob{
			filename: filename,
			pattern:  shardPattern(filename),
		})
	}

	label := fmt.Sprintf("%s — %s", repoID, filenames[0])
	if len(filenames) > 1 {
		label = fmt.Sprintf("%s — %d files", repoID, len(filenames))
	}
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
	d.totalBytes = totalBytes
	d.progress = nil

	ctx, cancelFn := context.WithCancel(context.Background())
	d.cancel = cancelFn

	log.Printf("download starting: repo=%s files=%v sidecars=%v", repoID, filenames, sidecarFiles)
	go d.run(ctx, repoID, repoDir, jobs, sidecarFiles)
	return nil
}

func (d *downloader) appendLine(line string) {
	d.mu.Lock()
	d.lines = append(d.lines, line)
	d.mu.Unlock()
}

// runHFCommand executes a single `hf` subprocess, streaming output to d.lines.
// Returns non-nil on failure; ctx.Err() is set when cancelled.
func (d *downloader) runHFCommand(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, "hf", args...)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return cmd.Process.Signal(os.Interrupt)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

	env := append(os.Environ(), "PYTHONUNBUFFERED=1")
	if d.cfg.HFToken != "" {
		env = append(env, "HF_TOKEN="+d.cfg.HFToken)
	}
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start hf: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		sc.Split(scanCRLF)
		for sc.Scan() {
			if line := strings.TrimSpace(ansiRe.ReplaceAllString(sc.Text(), "")); line != "" {
				d.appendLine(line)
			}
		}
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Split(scanCRLF)
		for sc.Scan() {
			if line := strings.TrimSpace(ansiRe.ReplaceAllString(sc.Text(), "")); line != "" {
				d.appendLine(line)
			}
		}
	}()
	wg.Wait()
	return cmd.Wait()
}

func (d *downloader) run(ctx context.Context, repoID, repoDir string, jobs []downloadJob, sidecarFiles []string) {
	d.appendLine(fmt.Sprintf("[w84ggufman] repo: %s", repoID))

	// Progress polling: measure repoDir as a whole.
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		startTime := time.Now()
		var lastBytes int64
		var lastMeasure time.Time
		for range ticker.C {
			d.mu.Lock()
			busy := d.busy
			total := d.totalBytes
			d.mu.Unlock()
			if !busy {
				return
			}
			now := time.Now()
			cur := dirSize(repoDir)
			speed := int64(0)
			if !lastMeasure.IsZero() {
				dt := now.Sub(lastMeasure).Seconds()
				if dt > 0 && cur > lastBytes {
					speed = int64(float64(cur-lastBytes) / dt)
				}
			}
			if speed == 0 {
				if elapsed := time.Since(startTime).Seconds(); elapsed > 0 {
					speed = int64(float64(cur) / elapsed)
				}
			}
			lastBytes = cur
			lastMeasure = now
			pct := -1
			eta := -1
			if total > 0 {
				pct = int(float64(cur) / float64(total) * 100)
				if pct > 99 {
					pct = 99
				}
				if speed > 0 && cur < total {
					eta = int(float64(total-cur) / float64(speed))
				}
			}
			d.mu.Lock()
			d.progress = &progressInfo{Pct: pct, Speed: speed, ETA: eta, DLBytes: cur}
			d.mu.Unlock()
		}
	}()

	if err := os.MkdirAll(repoDir, 0755); err != nil {
		d.finishWithError(fmt.Errorf("could not create model directory: %w", err))
		return
	}

	// Sidecars (mmproj, VAE, etc.) go to the same repoDir.
	if len(sidecarFiles) > 0 {
		d.appendLine(fmt.Sprintf("[w84ggufman] companions: %s", strings.Join(sidecarFiles, ", ")))
		d.appendLine("[w84ggufman] downloading companion files (initializing, please wait)...")
		sidecarArgs := []string{"download", repoID}
		for _, sf := range sidecarFiles {
			sidecarArgs = append(sidecarArgs, "--include", sf)
		}
		sidecarArgs = append(sidecarArgs, "--local-dir", repoDir)
		if err := d.runHFCommand(ctx, sidecarArgs); err != nil {
			if ctx.Err() != nil {
				d.appendLine("[w84ggufman] download cancelled")
				d.mu.Lock()
				d.busy = false
				d.cancel = nil
				d.progress = nil
				d.mu.Unlock()
				return
			}
			d.finishWithError(fmt.Errorf("companion download failed: %w", err))
			return
		}
	}

	for i, job := range jobs {
		if len(jobs) > 1 {
			d.mu.Lock()
			d.active = fmt.Sprintf("%s — %s (%d/%d)", repoID, job.filename, i+1, len(jobs))
			d.mu.Unlock()
		}

		d.appendLine(fmt.Sprintf("[w84ggufman] file: %s", job.pattern))
		if len(jobs) > 1 {
			d.appendLine(fmt.Sprintf("[w84ggufman] starting hf download %d/%d (initializing, please wait)...", i+1, len(jobs)))
		} else {
			d.appendLine("[w84ggufman] starting hf download (initializing, please wait)...")
		}

		args := []string{"download", repoID, "--include", job.pattern, "--local-dir", repoDir}
		if err := d.runHFCommand(ctx, args); err != nil {
			if ctx.Err() != nil {
				log.Printf("download cancelled: %s", job.filename)
				d.appendLine("[w84ggufman] download cancelled")
				d.mu.Lock()
				d.busy = false
				d.cancel = nil
				d.progress = nil
				d.mu.Unlock()
				return
			}
			d.finishWithError(fmt.Errorf("hf download failed: %w", err))
			return
		}
	}

	// Note: config file registration (models.ini / config.yaml) is no longer
	// automatic — the user manages it via the config editors.
	// if err := writeModelMeta(repoDir, repoID); err != nil { ... }
	// if err := d.preset.AddModel(...); err != nil { ... }
	// if d.llamaSwap != nil { d.llamaSwap.AddModel(...) }

	// Note: automatic service restart is no longer performed after download.
	// if err := restartService(d.cfg.LlamaService); err != nil { ... }

	log.Printf("download complete: %d file(s) from %s", len(jobs), repoID)
	d.appendLine("[w84ggufman] download complete")

	d.mu.Lock()
	d.busy = false
	d.cancel = nil
	d.progress = nil
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
		prog := d.progress
		d.mu.Unlock()

		for ; sent < len(snapshot); sent++ {
			writeSSEEvent(w, "line", snapshot[sent])
			flusher.Flush()
		}

		if prog != nil {
			writeSSEEvent(w, "progress", prog)
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
