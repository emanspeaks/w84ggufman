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
