package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
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

// ── AMD DRM ioctl ────────────────────────────────────────────────────────────
//
// DRM_IOCTL_AMDGPU_INFO = _IOW('d', 0x45, struct drm_amdgpu_info)
// sizeof(struct drm_amdgpu_info) == 32 on x86_64/arm64 (8+4+4+16-byte union).
// AMDGPU_INFO_MEMORY (0x19) returns drm_amdgpu_memory_info (3×4×8 = 96 bytes).

const drmIoctlAmdgpuInfo = uintptr(0x40206445)
const amdgpuInfoMemory = uint32(0x19)

type drmAmdgpuHeapInfo struct {
	TotalHeapSize  uint64
	UsableHeapSize uint64
	HeapUsage      uint64
	MaxAllocation  uint64
}

type drmAmdgpuMemoryInfo struct {
	VRAM          drmAmdgpuHeapInfo
	CpuAccessVRAM drmAmdgpuHeapInfo
	GTT           drmAmdgpuHeapInfo
}

// drmAmdgpuInfoReq matches struct drm_amdgpu_info from include/uapi/drm/amdgpu_drm.h.
// Layout: return_pointer(8) + return_size(4) + query(4) + union(16) = 32 bytes.
type drmAmdgpuInfoReq struct {
	ReturnPointer uint64
	ReturnSize    uint32
	Query         uint32
	_             [16]byte // union — no sub-fields needed for AMDGPU_INFO_MEMORY
}

// amdRenderDevice returns the path to the first amdgpu DRM render node, found
// once at first call and cached. Also populates amdDevPCIAddr for fdinfo use.
// Logs success or failure once to the system log.
var (
	amdDevOnce    sync.Once
	amdDevPath    string
	amdDevPCIAddr string // PCI address e.g. "0000:c1:00.0", for fdinfo fallback
)

func amdRenderDevice() string {
	amdDevOnce.Do(func() {
		driverLinks, _ := filepath.Glob("/sys/class/drm/card*/device/driver")
		for _, link := range driverLinks {
			target, err := os.Readlink(link)
			if err != nil || !strings.Contains(filepath.Base(target), "amdgpu") {
				continue
			}
			cardName := filepath.Base(filepath.Dir(filepath.Dir(link))) // e.g. "card1"
			if !strings.HasPrefix(cardName, "card") {
				continue
			}

			// Resolve the PCI device path. Render minor numbers are assigned only
			// to render-capable DRM devices and don't always equal 128+card_number.
			// On APUs where card0 is display-only, card1 gets renderD128 not D129.
			// Walking the sysfs device hierarchy finds the correct node directly.
			pciDev, pciErr := filepath.EvalSymlinks("/sys/class/drm/" + cardName + "/device")
			if pciErr == nil && amdDevPCIAddr == "" {
				amdDevPCIAddr = filepath.Base(pciDev)
			}
			if pciErr == nil {
				nodes, _ := filepath.Glob(pciDev + "/drm/renderD*")
				for _, node := range nodes {
					renderPath := "/dev/dri/" + filepath.Base(node)
					if _, err := os.Stat(renderPath); err == nil {
						amdDevPath = renderPath
						log.Printf("VRAM: AMD render device: %s (card %s)", renderPath, cardName)
						return
					}
				}
			}

			// Fallback: assume renderD(128+N) for cardN.
			n, err := strconv.ParseUint(cardName[4:], 10, 32)
			if err != nil {
				continue
			}
			renderPath := "/dev/dri/renderD" + strconv.FormatUint(128+n, 10)
			if _, err := os.Stat(renderPath); err == nil {
				amdDevPath = renderPath
				log.Printf("VRAM: AMD render device: %s (card %s, by number)", renderPath, cardName)
				return
			}
			log.Printf("VRAM: amdgpu found on %s but no render node found", cardName)
		}
		if amdDevPath == "" {
			if len(driverLinks) == 0 {
				log.Printf("VRAM: no DRM card entries in sysfs; AMD usage unavailable")
			} else {
				log.Printf("VRAM: no amdgpu render node found among %d DRM card(s)", len(driverLinks))
			}
		}
	})
	return amdDevPath
}

// amdFDInfoVRAMUsed returns total AMD GPU VRAM used by reading /proc/*/fdinfo/*
// entries for the GPU at pciAddr. This is the approach used by nvtop and works
// even when the DRM ioctl path is unavailable or returns incomplete data.
// Usage is summed per unique drm-client-id to avoid double-counting clients
// that have multiple file descriptors open to the same device.
func amdFDInfoVRAMUsed(pciAddr string) (uint64, bool) {
	fdinfos, _ := filepath.Glob("/proc/[0-9]*/fdinfo/*")
	seen := make(map[string]uint64) // drm-client-id → max vram KiB seen

	for _, path := range fdinfos {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var clientID string
		var vramKiB uint64
		matchesDev := false

		for _, line := range strings.Split(string(data), "\n") {
			k, v, ok := strings.Cut(line, ":\t")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "drm-pdev":
				matchesDev = strings.TrimSpace(v) == pciAddr
			case "drm-client-id":
				clientID = strings.TrimSpace(v)
			case "drm-memory-vram":
				if f := strings.Fields(v); len(f) >= 1 {
					if n, err2 := strconv.ParseUint(f[0], 10, 64); err2 == nil {
						vramKiB = n
					}
				}
			}
		}
		if matchesDev && clientID != "" {
			if existing, ok2 := seen[clientID]; !ok2 || vramKiB > existing {
				seen[clientID] = vramKiB
			}
		}
	}

	if len(seen) == 0 {
		return 0, false
	}
	var total uint64
	for _, kib := range seen {
		total += kib * 1024
	}
	return total, true
}

// queryAMDGPUVRAMUsed opens the given DRM render node and issues the
// AMDGPU_INFO_MEMORY ioctl to read current VRAM heap usage.
func queryAMDGPUVRAMUsed(devicePath string) (uint64, bool) {
	f, err := os.Open(devicePath)
	if err != nil {
		log.Printf("VRAM: open %s: %v", devicePath, err)
		return 0, false
	}
	defer f.Close()

	var mem drmAmdgpuMemoryInfo
	req := drmAmdgpuInfoReq{
		ReturnPointer: uint64(uintptr(unsafe.Pointer(&mem))),
		ReturnSize:    uint32(unsafe.Sizeof(mem)),
		Query:         amdgpuInfoMemory,
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		f.Fd(),
		drmIoctlAmdgpuInfo,
		uintptr(unsafe.Pointer(&req)),
	)
	if errno != 0 {
		log.Printf("VRAM: AMDGPU_INFO_MEMORY ioctl on %s: errno %d", devicePath, errno)
		return 0, false
	}
	return mem.VRAM.HeapUsage, true
}

// detectVRAMUsedBytes probes the system for current GPU memory usage in bytes.
// Returns (used, true) on success, (0, false) when measurement is not available.
func detectVRAMUsedBytes() (uint64, bool) {
	// NVIDIA via nvidia-smi
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=memory.used", "--format=csv,noheader,nounits").Output(); err == nil {
		var total uint64
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if mib, err := strconv.ParseUint(strings.TrimSpace(line), 10, 64); err == nil {
				total += mib << 20 // MiB → bytes
			}
		}
		if total > 0 {
			return total, true
		}
	}

	// AMD: try DRM ioctl first (AMDGPU_INFO_MEMORY), fall back to fdinfo.
	// The ioctl requires render-group access; fdinfo (/proc/*/fdinfo/*) works
	// without it and is the approach used by nvtop on unified-memory APUs.
	dev := amdRenderDevice() // also populates amdDevPCIAddr
	if dev != "" {
		if used, ok := queryAMDGPUVRAMUsed(dev); ok {
			return used, true
		}
	}
	if amdDevPCIAddr != "" {
		if used, ok := amdFDInfoVRAMUsed(amdDevPCIAddr); ok {
			return used, true
		}
	}

	// Apple Silicon: iogpu.wired_size is the current GPU-wired memory allocation in bytes.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	if out, err := exec.CommandContext(ctx2, "sysctl", "-n", "iogpu.wired_size").Output(); err == nil {
		if b, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64); err == nil && b > 0 {
			return b, true
		}
	}

	return 0, false
}
