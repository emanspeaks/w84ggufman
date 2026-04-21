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

type atopwebGPUPctEntry struct {
	GpuPct float64 `json:"gpu_pct"`
}

// atopwebWasOK tracks the last known connection state so we only log transitions.
// -1 = never probed, 0 = last probe failed, 1 = last probe succeeded.
var atopwebWasOK = -1

// probeAtopwebVRAM queries baseURL/api/vram and returns the summed total and
// used VRAM in bytes. Returns (0, 0, false) on any error or timeout.
// Logs only on state transitions (connected → error or error → connected).
func probeAtopwebVRAM(baseURL string) (totalBytes, usedBytes uint64, ok bool) {
	if baseURL == "" {
		return 0, 0, false
	}
	url := strings.TrimRight(baseURL, "/") + "/api/vram"

	total, used, success, reason := doProbe(url)
	if success {
		switch atopwebWasOK {
		case -1:
			log.Printf("atopweb startup: connected to %s", url)
		case 0:
			log.Printf("atopweb connection restored: %s", url)
		}
		atopwebWasOK = 1
		return total, used, true
	}
	switch atopwebWasOK {
	case -1:
		log.Printf("atopweb startup: unreachable (%s)", reason)
	case 1:
		log.Printf("atopweb connection lost: %s", reason)
	}
	atopwebWasOK = 0
	return 0, 0, false
}

// probeAtopwebGPUPct queries baseURL/api/gpu-pct and returns the average GPU
// utilisation across all reported GPUs. No connection-state logging here; the
// VRAM probe already owns that.
func probeAtopwebGPUPct(baseURL string) (pct float64, ok bool) {
	if baseURL == "" {
		return 0, false
	}
	url := strings.TrimRight(baseURL, "/") + "/api/gpu-pct"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return 0, false
	}
	defer resp.Body.Close()
	var entries []atopwebGPUPctEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil || len(entries) == 0 {
		return 0, false
	}
	var sum float64
	for _, e := range entries {
		sum += e.GpuPct
	}
	return sum / float64(len(entries)), true
}

func doProbe(url string) (totalBytes, usedBytes uint64, ok bool, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, 0, false, err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, false, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, false, "status " + resp.Status
	}
	var gpus []atopwebGPU
	if err := json.NewDecoder(resp.Body).Decode(&gpus); err != nil {
		return 0, 0, false, "decode error: " + err.Error()
	}
	if len(gpus) == 0 {
		return 0, 0, false, "empty GPU list"
	}
	var totalMiB, usedMiB float64
	for _, g := range gpus {
		totalMiB += g.TotalMiB
		usedMiB += g.UsedMiB
	}
	const mib = 1024 * 1024
	return uint64(totalMiB * mib), uint64(usedMiB * mib), true, ""
}
