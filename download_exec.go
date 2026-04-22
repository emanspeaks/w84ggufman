package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

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

func buildDownloadLabel(repoID string, filenames []string, sidecarFiles []string) string {
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
	return label
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isDuplicateLocked returns true if the given repoID+filenames combo already
// exists as the active download or anywhere in the queue. Must be called with
// d.mu held.
func (d *downloader) isDuplicateLocked(repoID string, filenames []string) bool {
	if d.activeRepoID == repoID && slicesEqual(d.activeFilenames, filenames) {
		return true
	}
	for _, e := range d.queue {
		if e.repoID == repoID && slicesEqual(e.filenames, filenames) {
			return true
		}
	}
	return false
}

// startLocked arms the downloader for a new download and spawns the run
// goroutine. Must be called with d.mu held; does NOT release the lock.
func (d *downloader) startLocked(repoID string, filenames []string, sidecarFiles []string, totalBytes int64, label string) {
	repoDir := filepath.Join(d.cfg.ModelsDir, filepath.FromSlash(repoID))
	jobs := make([]downloadJob, 0, len(filenames))
	for _, filename := range filenames {
		jobs = append(jobs, downloadJob{
			filename: filename,
			pattern:  shardPattern(filename),
		})
	}

	d.active = label
	d.activeRepoID = repoID
	d.activeFilenames = append([]string(nil), filenames...)
	d.busy = true
	d.lines = nil
	d.totalBytes = totalBytes
	d.progress = nil

	ctx, cancelFn := context.WithCancel(context.Background())
	d.cancel = cancelFn

	log.Printf("download starting: repo=%s files=%v sidecars=%v", repoID, filenames, sidecarFiles)
	go d.run(ctx, repoID, repoDir, jobs, append([]string(nil), sidecarFiles...))
}

// start enqueues or immediately begins a download.
// Returns (queued=true) if the request was added to the queue (or was already
// present), (queued=false) if it started executing right away.
func (d *downloader) start(repoID string, filenames []string, sidecarFiles []string, totalBytes int64, _ bool) (bool, error) {
	if len(filenames) == 0 {
		return false, fmt.Errorf("at least one filename is required")
	}
	for _, filename := range filenames {
		if strings.Contains(filename, "..") || strings.HasPrefix(filename, "/") {
			return false, fmt.Errorf("invalid filename: %s", filename)
		}
	}
	for _, sf := range sidecarFiles {
		if strings.Contains(sf, "..") || strings.HasPrefix(sf, "/") {
			return false, fmt.Errorf("invalid sidecar filename: %s", sf)
		}
	}

	label := buildDownloadLabel(repoID, filenames, sidecarFiles)

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.busy {
		if d.isDuplicateLocked(repoID, filenames) {
			return true, nil // already present; silently accept
		}
		d.nextID++
		d.queue = append(d.queue, queueEntry{
			id:           d.nextID,
			repoID:       repoID,
			filenames:    append([]string(nil), filenames...),
			sidecarFiles: append([]string(nil), sidecarFiles...),
			totalBytes:   totalBytes,
			label:        label,
		})
		d.queueVer++
		log.Printf("download enqueued (position %d): repo=%s files=%v", len(d.queue), repoID, filenames)
		return true, nil
	}

	d.startLocked(repoID, filenames, sidecarFiles, totalBytes, label)
	return false, nil
}

// advanceQueue atomically starts the next queued download or, when the queue
// is empty, marks the downloader as idle. Must NOT be called with d.mu held.
func (d *downloader) advanceQueue() {
	d.mu.Lock()
	if len(d.queue) == 0 {
		d.busy = false
		d.cancel = nil
		d.progress = nil
		d.activeRepoID = ""
		d.activeFilenames = nil
		d.queueVer++
		d.mu.Unlock()
		return
	}
	next := d.queue[0]
	d.queue = d.queue[1:]
	d.queueVer++
	d.active = next.label
	d.activeRepoID = next.repoID
	d.activeFilenames = append([]string(nil), next.filenames...)
	d.totalBytes = next.totalBytes
	d.progress = nil
	newCtx, cancelFn := context.WithCancel(context.Background())
	d.cancel = cancelFn
	d.mu.Unlock()

	repoDir := filepath.Join(d.cfg.ModelsDir, filepath.FromSlash(next.repoID))
	jobs := make([]downloadJob, 0, len(next.filenames))
	for _, f := range next.filenames {
		jobs = append(jobs, downloadJob{filename: f, pattern: shardPattern(f)})
	}
	d.appendLine(fmt.Sprintf("[w84ggufman] next in queue: %s", next.label))
	go d.run(newCtx, next.repoID, repoDir, jobs, next.sidecarFiles)
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
				d.queue = nil
				d.queueVer++
				d.activeRepoID = ""
				d.activeFilenames = nil
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
				d.queue = nil
				d.queueVer++
				d.activeRepoID = ""
				d.activeFilenames = nil
				d.mu.Unlock()
				return
			}
			d.finishWithError(fmt.Errorf("hf download failed: %w", err))
			return
		}
	}

	// Note: config file registration (models.ini / config.yaml) is no longer
	// automatic — the user manages it via the config editors.

	// Note: automatic service restart is no longer performed after download.

	log.Printf("download complete: %d file(s) from %s", len(jobs), repoID)
	d.appendLine("[w84ggufman] download complete")

	// Atomically advance to next queued download, or go idle.
	d.advanceQueue()
}
