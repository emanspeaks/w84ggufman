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

type progressInfo struct {
	Pct     int   `json:"pct"`     // 0–100; -1 when total is unknown
	Speed   int64 `json:"speed"`   // bytes/sec
	ETA     int   `json:"eta"`     // seconds remaining; -1 when unknown
	DLBytes int64 `json:"dlBytes"` // bytes downloaded so far
}

type downloader struct {
	cfg        Config
	preset     *presetManager
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

// modelDirName derives the local directory name for a set of files being
// downloaded together. For a single file it uses the existing behaviour
// (strip .gguf and shard suffix). For multiple files it finds the common
// prefix of all stems and strips trailing separators so that, for example,
// ["Model-Q4_K_M.gguf","Model-Q8_0.gguf"] maps to the directory "Model".
func modelDirName(filenames []string) string {
	if len(filenames) == 0 {
		return ""
	}
	if len(filenames) == 1 {
		return modelNameFromFilename(filenames[0])
	}
	stems := make([]string, len(filenames))
	for i, f := range filenames {
		stems[i] = strings.ToLower(modelNameFromFilename(f))
	}
	pfxLen := len(stems[0])
	for _, s := range stems[1:] {
		if len(s) < pfxLen {
			pfxLen = len(s)
		}
		for i := 0; i < pfxLen; i++ {
			if stems[0][i] != s[i] {
				pfxLen = i
				break
			}
		}
	}
	first := modelNameFromFilename(filenames[0])
	name := strings.TrimRight(first[:pfxLen], "-_")
	if name == "" {
		name = first
	}
	return name
}

// start begins a download. If force is true and the model directory already
// exists, the existing directory is renamed to <dir>.old before downloading;
// it is restored on failure and deleted on success.
func (d *downloader) start(repoID string, filenames []string, sidecarFiles []string, totalBytes int64, force bool) error {
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
	modelName := modelDirName(filenames)
	if modelName == "" || strings.ContainsAny(modelName, "/\\") {
		return fmt.Errorf("could not derive valid model name from filenames")
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
		_ = removeAllWritable(oldDir)
		if err := os.Rename(destDir, oldDir); err != nil {
			return fmt.Errorf("could not move existing model: %w", err)
		}
	}

	patterns := make([]string, len(filenames))
	for i, f := range filenames {
		patterns[i] = shardPattern(f)
	}
	label := fmt.Sprintf("%s — %s", repoID, filenames[0])
	if len(filenames) > 1 {
		label = fmt.Sprintf("%s — %d quants", repoID, len(filenames))
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

	log.Printf("download starting: repo=%s files=%v sidecars=%v dir=%s", repoID, filenames, sidecarFiles, destDir)
	go d.run(ctx, repoID, patterns, sidecarFiles, destDir, modelName, oldDir)
	return nil
}

func (d *downloader) appendLine(line string) {
	d.mu.Lock()
	d.lines = append(d.lines, line)
	d.mu.Unlock()
}

func (d *downloader) run(ctx context.Context, repoID string, patterns []string, sidecarFiles []string, destDir, modelName, oldDir string) {
	args := []string{"download", repoID}
	for _, p := range patterns {
		args = append(args, "--include", p)
	}
	for _, sf := range sidecarFiles {
		args = append(args, "--include", sf)
	}
	args = append(args, "--local-dir", destDir)

	d.appendLine(fmt.Sprintf("[w84ggufman] repo: %s", repoID))
	d.appendLine(fmt.Sprintf("[w84ggufman] files: %s", strings.Join(patterns, ", ")))
	if len(sidecarFiles) > 0 {
		d.appendLine(fmt.Sprintf("[w84ggufman] companions: %s", strings.Join(sidecarFiles, ", ")))
	}
	d.appendLine("[w84ggufman] starting hf download (initializing, please wait)...")

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

	env := append(os.Environ(), "PYTHONUNBUFFERED=1")
	if d.cfg.HFToken != "" {
		env = append(env, "HF_TOKEN="+d.cfg.HFToken)
	}
	cmd.Env = env

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

	// Watch the destination directory size to report byte-level progress.
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
			cur := dirSize(destDir)
			speed := int64(0)
			if !lastMeasure.IsZero() {
				dt := now.Sub(lastMeasure).Seconds()
				if dt > 0 && cur > lastBytes {
					speed = int64(float64(cur-lastBytes) / dt)
				}
			}
			// Fall back to average speed when instantaneous is zero.
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

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			log.Printf("download cancelled: %s", modelName)
			d.appendLine("[w84ggufman] download cancelled")
		} else {
			d.restoreOnFailure(oldDir, destDir)
			d.finishWithError(fmt.Errorf("hf download failed: %w", err))
			return
		}
		d.restoreOnFailure(oldDir, destDir)
		d.mu.Lock()
		d.busy = false
		d.cancel = nil
		d.progress = nil
		d.mu.Unlock()
		return
	}

	// Success: write metadata, clean up old dir, update managed.ini.
	if err := writeModelMeta(destDir, repoID); err != nil {
		d.appendLine(fmt.Sprintf("[w84ggufman] warning: could not write metadata: %v", err))
	}
	if oldDir != "" {
		if err := removeAllWritable(oldDir); err != nil {
			d.appendLine(fmt.Sprintf("[w84ggufman] warning: could not remove old model: %v", err))
		}
	}

	// Use destDir when multiple files are downloaded or the pattern is a glob.
	modelPath := destDir
	if len(patterns) == 1 && !strings.Contains(patterns[0], "*") {
		modelPath = filepath.Join(destDir, patterns[0])
	}
	mmprojPath := ""
	for _, sf := range sidecarFiles {
		if matchesMmproj(sf) {
			mmprojPath = filepath.Join(destDir, filepath.Base(sf))
			break
		}
	}
	if err := d.preset.AddModel(modelName, modelPath, mmprojPath); err != nil {
		d.appendLine(fmt.Sprintf("[w84ggufman] warning: could not update managed.ini: %v", err))
	}

	log.Printf("download complete: %s", modelName)
	d.appendLine("[w84ggufman] download complete, restarting service...")
	if err := restartService(d.cfg.LlamaService); err != nil {
		d.appendLine(fmt.Sprintf("[w84ggufman] warning: failed to restart service: %v", err))
	} else {
		d.appendLine("[w84ggufman] service restarted successfully")
	}

	d.mu.Lock()
	d.busy = false
	d.cancel = nil
	d.progress = nil
	d.mu.Unlock()
}

// restoreOnFailure removes any partial destDir and renames oldDir back.
func (d *downloader) restoreOnFailure(oldDir, destDir string) {
	if oldDir == "" {
		return
	}
	_ = removeAllWritable(destDir)
	if err := os.Rename(oldDir, destDir); err != nil {
		d.appendLine(fmt.Sprintf("[w84ggufman] warning: could not restore old model: %v", err))
	} else {
		d.appendLine("[w84ggufman] restored previous model after failed download")
	}
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
