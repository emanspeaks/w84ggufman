package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

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
