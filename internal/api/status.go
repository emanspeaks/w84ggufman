package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

type statusResponse struct {
	LlamaReachable     bool         `json:"llamaReachable"`
	DownloadInProgress bool         `json:"downloadInProgress"`
	ActiveDownload     string       `json:"activeDownload"`
	Version            string       `json:"version"`
	Disk               diskInfo     `json:"disk"`
	WarnDownloadBytes  uint64       `json:"warnDownloadBytes"`
	VramBytes          uint64       `json:"vramBytes"`
	VramUsedBytes      uint64       `json:"vramUsedBytes"`
	VramUsedKnown      bool         `json:"vramUsedKnown"`
	WarnVramBytes      uint64       `json:"warnVramBytes"`
	LoadedModels       []string     `json:"loadedModels"`
	LlamaSwapEnabled   bool         `json:"llamaSwapEnabled"`
	LlamaServiceLabel  string       `json:"llamaServiceLabel"`
	AtopwebURL         string       `json:"atopwebURL,omitempty"`
	GpuPct             float64      `json:"gpuPct"`
	GpuPctKnown        bool         `json:"gpuPctKnown"`
	Queue              []QueueEntry `json:"queue"`
}

func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	reachable := true
	var loadedIDs []string
	resp, err := http.Get(s.cfg.LlamaServerURL + "/v1/models")
	if err != nil {
		reachable = false
	} else {
		var lmr llamaModelsResponse
		if err := json.NewDecoder(resp.Body).Decode(&lmr); err == nil {
			for _, m := range lmr.Data {
				loadedIDs = append(loadedIDs, m.ID)
			}
		}
		resp.Body.Close()
	}

	active, inProgress := s.dl.ActiveInfo()
	disk, _ := getDiskInfo(s.cfg.ModelsDir)
	warnBytes := uint64(s.cfg.WarnDownloadGiB * 1024 * 1024 * 1024)
	pct := s.cfg.WarnVramPercent
	if pct <= 0 {
		pct = 80
	}

	// Resolve total and used VRAM. Atopweb is tried first when a URL is
	// configured; on failure we fall back to system-level detection.
	vramTotal := s.vramBytes
	var vramUsed uint64
	var vramUsedKnown bool
	var gpuPct float64
	var gpuPctKnown bool
	if s.cfg.AtopwebURL != "" {
		if t, u, ok := probeAtopwebVRAM(s.cfg.AtopwebURL); ok {
			if t > 0 {
				vramTotal = t
			}
			vramUsed = u
			vramUsedKnown = true
		}
		gpuPct, gpuPctKnown = probeAtopwebGPUPct(s.cfg.AtopwebURL)
	}
	if !vramUsedKnown && vramTotal > 0 && s.deps.DetectVRAMUsedBytes != nil {
		vramUsed, vramUsedKnown = s.deps.DetectVRAMUsedBytes(s.cfg.LlamaService)
	}

	warnVram := uint64(float64(vramTotal) * pct / 100)

	writeJSON(w, statusResponse{
		LlamaReachable:     reachable,
		DownloadInProgress: inProgress,
		ActiveDownload:     active,
		Version:            s.deps.Version,
		Disk:               disk,
		WarnDownloadBytes:  warnBytes,
		VramBytes:          vramTotal,
		VramUsedBytes:      vramUsed,
		VramUsedKnown:      vramUsedKnown,
		WarnVramBytes:      warnVram,
		LoadedModels:       loadedIDs,
		LlamaSwapEnabled:   s.llamaSwap != nil,
		LlamaServiceLabel:  strings.TrimSuffix(s.cfg.LlamaService, ".service"),
		AtopwebURL:         s.cfg.AtopwebURL,
		GpuPct:             gpuPct,
		GpuPctKnown:        gpuPctKnown,
		Queue:              s.dl.QueueEntries(),
	})
}
