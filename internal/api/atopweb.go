package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

type atopwebSystemResponse struct {
	MemInfoKb struct {
		MemTotal     uint64 `json:"MemTotal"`
		MemAvailable uint64 `json:"MemAvailable"`
	} `json:"meminfo_kb"`
	DrmMem struct {
		VramTotalKib uint64 `json:"vram_total_kib"`
		VramUsedKib  uint64 `json:"vram_used_kib"`
	} `json:"drm_mem"`
}

type atopwebGPUPctEntry struct {
	GpuPct float64 `json:"gpu_pct"`
}

// atopwebWasOK tracks the last known connection state so we only log transitions.
// -1 = never probed, 0 = last probe failed, 1 = last probe succeeded.
var atopwebWasOK = -1

// probeAtopwebSystem queries baseURL/api/system and returns total and used
// memory in bytes. Total = system RAM + VRAM; used = VRAM used + system RAM
// used (MemTotal - MemAvailable). Returns (0, 0, false) on any error or timeout.
// Logs only on state transitions (connected → error or error → connected).
func probeAtopwebSystem(baseURL string) (totalBytes, usedBytes uint64, ok bool) {
	if baseURL == "" {
		return 0, 0, false
	}
	url := strings.TrimRight(baseURL, "/") + "/api/system"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		atopwebWasOK = 0
		return 0, 0, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		switch atopwebWasOK {
		case -1:
			log.Printf("atopweb startup: unreachable (%v)", err)
		case 1:
			log.Printf("atopweb connection lost: %v", err)
		}
		atopwebWasOK = 0
		return 0, 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		reason := "status " + resp.Status
		switch atopwebWasOK {
		case -1:
			log.Printf("atopweb startup: unreachable (%s)", reason)
		case 1:
			log.Printf("atopweb connection lost: %s", reason)
		}
		atopwebWasOK = 0
		return 0, 0, false
	}
	var sys atopwebSystemResponse
	if err := json.NewDecoder(resp.Body).Decode(&sys); err != nil || sys.MemInfoKb.MemTotal == 0 {
		atopwebWasOK = 0
		return 0, 0, false
	}

	switch atopwebWasOK {
	case -1:
		log.Printf("atopweb startup: connected to %s", url)
	case 0:
		log.Printf("atopweb connection restored: %s", url)
	}
	atopwebWasOK = 1

	totalKib := sys.MemInfoKb.MemTotal + sys.DrmMem.VramTotalKib
	usedKib := sys.DrmMem.VramUsedKib + sys.MemInfoKb.MemTotal - sys.MemInfoKb.MemAvailable
	return totalKib * 1024, usedKib * 1024, true
}

// probeAtopwebGPUPct queries baseURL/api/gpu-pct and returns the average GPU
// utilisation across all reported GPUs. No connection-state logging here; the
// system probe already owns that.
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
