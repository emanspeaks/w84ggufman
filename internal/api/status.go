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
	RamTotalBytes      uint64       `json:"ramTotalBytes"`
	RamUsedBytes       uint64       `json:"ramUsedBytes"`
	RamKnown           bool         `json:"ramKnown"`
	WarnRamBytes       uint64       `json:"warnRamBytes"`
	LoadedModels       []string     `json:"loadedModels"`
	LlamaSwapEnabled   bool         `json:"llamaSwapEnabled"`
	LlamaServiceLabel  string       `json:"llamaServiceLabel"`
	AtopwebURL         string       `json:"atopwebURL,omitempty"`
	LlamaServerURL         string       `json:"llamaServerURL,omitempty"`
	LlamaServerLandingPage string       `json:"llamaServerLandingPage,omitempty"`
	GpuPct             float64      `json:"gpuPct"`
	GpuPctKnown        bool         `json:"gpuPctKnown"`
	Queue              []QueueEntry `json:"queue"`
	UpdatesAvailable   int          `json:"updatesAvailable"`
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
	warnRamPct := s.cfg.WarnRamPercent
	if warnRamPct <= 0 {
		warnRamPct = 80
	}

	// Resolve total and used RAM using atopweb if available
	var ramTotal uint64
	var ramUsed uint64
	var ramKnown bool
	var gpuPct float64
	var gpuPctKnown bool
	if s.cfg.AtopwebURL != "" {
		ramTotal, ramUsed, ramKnown = probeAtopwebSystem(s.cfg.AtopwebURL)
		gpuPct, gpuPctKnown = probeAtopwebGPUPct(s.cfg.AtopwebURL)
	}

	effectiveRamTotal := ramTotal
	if effectiveRamTotal == 0 {
		effectiveRamTotal = uint64(s.cfg.RamGiB * 1024 * 1024 * 1024)
	}
	warnRamBytes := uint64(float64(effectiveRamTotal) * warnRamPct / 100)

	updatesAvailable := 0
	if s.deps.PendingUpdateCount != nil {
		updatesAvailable = s.deps.PendingUpdateCount()
	}
	writeJSON(w, statusResponse{
		LlamaReachable:         reachable,
		DownloadInProgress:     inProgress,
		ActiveDownload:         active,
		Version:                s.deps.Version,
		Disk:                   disk,
		WarnDownloadBytes:      warnBytes,
		RamTotalBytes:          ramTotal,
		RamUsedBytes:           ramUsed,
		RamKnown:               ramKnown,
		WarnRamBytes:           warnRamBytes,
		LoadedModels:           loadedIDs,
		LlamaSwapEnabled:       s.llamaSwap != nil,
		LlamaServiceLabel:      strings.TrimSuffix(s.cfg.LlamaService, ".service"),
		AtopwebURL:             s.cfg.AtopwebURL,
		LlamaServerURL:         s.cfg.LlamaServerURL,
		LlamaServerLandingPage: s.cfg.LlamaServerLandingPage,
		GpuPct:                 gpuPct,
		GpuPctKnown:            gpuPctKnown,
		Queue:                  s.dl.QueueEntries(),
		UpdatesAvailable:       updatesAvailable,
	})
}
