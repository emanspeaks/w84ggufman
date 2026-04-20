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

	// AMD APU / unified memory: total GPU-accessible memory is the TTM dynamic
	// pool (pages_limit * 4 KiB) plus the BIOS-reserved carve-out reported by
	// mem_info_vram_total. On a Strix Halo the carve-out is ~0.5 GiB and the
	// TTM pool is the bulk; together they match what nvtop reports as total VRAM.
	// We treat any TTM value > 1 GiB as a deliberate unified-memory allocation.
	if data, err := os.ReadFile("/sys/module/ttm/parameters/pages_limit"); err == nil {
		if pages, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64); err == nil && pages > 0 {
			b := pages * 4096
			if b > 1<<30 {
				var carveOut uint64
				if matches, _ := filepath.Glob("/sys/class/drm/card*/device/mem_info_vram_total"); len(matches) > 0 {
					for _, p := range matches {
						if d, err := os.ReadFile(p); err == nil {
							if v, err := strconv.ParseUint(strings.TrimSpace(string(d)), 10, 64); err == nil {
								carveOut += v
							}
						}
					}
				}
				total := b + carveOut
				log.Printf("VRAM: detected %.3f GiB via TTM pages_limit + sysfs carve-out (AMD unified memory)", float64(total)/(1<<30))
				return total
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
			log.Printf("VRAM: detected %.3f GiB via sysfs mem_info_vram_total (AMD)", float64(total)/(1<<30))
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

// AMD ioctl helpers, fdinfo readers, detectVRAMUsedBytes: see vram_unused.go (//go:build ignore)
