package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

type atopwebGPU struct {
	TotalMiB float64 `json:"total_mib"`
	UsedMiB  float64 `json:"used_mib"`
}

// probeAtopwebVRAM queries baseURL/api/vram and returns the summed total and
// used VRAM in bytes. Returns (0, 0, false) on any error or timeout.
func probeAtopwebVRAM(baseURL string) (totalBytes, usedBytes uint64, ok bool) {
	if baseURL == "" {
		return 0, 0, false
	}
	url := strings.TrimRight(baseURL, "/") + "/api/vram"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, 0, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("atopweb probe failed: %v", err)
		return 0, 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("atopweb probe: unexpected status %s from %s", resp.Status, url)
		return 0, 0, false
	}
	var gpus []atopwebGPU
	if err := json.NewDecoder(resp.Body).Decode(&gpus); err != nil {
		log.Printf("atopweb probe: failed to decode response: %v", err)
		return 0, 0, false
	}
	if len(gpus) == 0 {
		log.Printf("atopweb probe: empty GPU list from %s", url)
		return 0, 0, false
	}
	var totalMiB, usedMiB float64
	for _, g := range gpus {
		totalMiB += g.TotalMiB
		usedMiB += g.UsedMiB
	}
	const mib = 1024 * 1024
	return uint64(totalMiB * mib), uint64(usedMiB * mib), true
}
