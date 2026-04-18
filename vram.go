package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// detectVRAMBytes probes the system for total GPU memory using platform-
// specific methods, trying NVIDIA → AMD unified → AMD discrete → Apple in order.
// Returns 0 if nothing is detected; caller should fall back to config.
func detectVRAMBytes() uint64 {
	// NVIDIA via nvidia-smi
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=memory.total", "--format=csv,noheader,nounits").Output(); err == nil {
		var total uint64
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if mib, err := strconv.ParseUint(strings.TrimSpace(line), 10, 64); err == nil {
				total += mib << 20 // MiB → bytes
			}
		}
		if total > 0 {
			log.Printf("VRAM: detected %.1f GiB via nvidia-smi", float64(total)/(1<<30))
			return total
		}
	}

	// AMD APU / unified memory: TTM pages_limit is the active pool allocation
	// in 4 KiB pages, set by the ttm.pages_limit=N kernel parameter.
	// This reflects the actual VRAM limit in effect, unlike mem_info_vram_total
	// which reports the full hardware RAM capacity on unified-memory systems.
	// We treat any value > 1 GiB as a deliberate unified-memory VRAM allocation.
	if data, err := os.ReadFile("/sys/module/ttm/parameters/pages_limit"); err == nil {
		if pages, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64); err == nil && pages > 0 {
			b := pages * 4096
			if b > 1<<30 { // > 1 GiB: treat as a real VRAM allocation
				log.Printf("VRAM: detected %.1f GiB via TTM pages_limit (AMD unified memory)", float64(b)/(1<<30))
				return b
			}
		}
	}

	// AMD discrete GPU on Linux via sysfs (reports actual VRAM chip size).
	if matches, _ := filepath.Glob("/sys/class/drm/card*/device/mem_info_vram_total"); len(matches) > 0 {
		var total uint64
		for _, p := range matches {
			if data, err := os.ReadFile(p); err == nil {
				if b, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64); err == nil {
					total += b
				}
			}
		}
		if total > 0 {
			log.Printf("VRAM: detected %.1f GiB via sysfs mem_info_vram_total (AMD)", float64(total)/(1<<30))
			return total
		}
	}

	// Apple Silicon unified memory via sysctl
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	if out, err := exec.CommandContext(ctx2, "sysctl", "-n", "hw.memsize").Output(); err == nil {
		if b, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64); err == nil && b > 0 {
			log.Printf("VRAM: detected %.1f GiB via sysctl hw.memsize (Apple unified memory)", float64(b)/(1<<30))
			return b
		}
	}

	log.Printf("VRAM: could not auto-detect; set vramGiB in config to enable per-quant warnings")
	return 0
}
