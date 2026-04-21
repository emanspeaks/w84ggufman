package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

type statusResponse struct {
	LlamaReachable     bool     `json:"llamaReachable"`
	DownloadInProgress bool     `json:"downloadInProgress"`
	ActiveDownload     string   `json:"activeDownload"`
	Version            string   `json:"version"`
	Disk               diskInfo `json:"disk"`
	WarnDownloadBytes  uint64   `json:"warnDownloadBytes"`
	VramBytes          uint64   `json:"vramBytes"`
	VramUsedBytes      uint64   `json:"vramUsedBytes"`
	VramUsedKnown      bool     `json:"vramUsedKnown"`
	WarnVramBytes      uint64   `json:"warnVramBytes"`
	LoadedModels       []string `json:"loadedModels"`
	LlamaSwapEnabled   bool     `json:"llamaSwapEnabled"`
	LlamaServiceLabel  string   `json:"llamaServiceLabel"`
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
	warnVram := uint64(float64(s.vramBytes) * pct / 100)
	writeJSON(w, statusResponse{
		LlamaReachable:     reachable,
		DownloadInProgress: inProgress,
		ActiveDownload:     active,
		Version:            s.deps.Version,
		Disk:               disk,
		WarnDownloadBytes:  warnBytes,
		VramBytes:          s.vramBytes,
		VramUsedBytes:      0,
		VramUsedKnown:      false,
		WarnVramBytes:      warnVram,
		LoadedModels:       loadedIDs,
		LlamaSwapEnabled:   s.llamaSwap != nil,
		LlamaServiceLabel:  strings.TrimSuffix(s.cfg.LlamaService, ".service"),
	})
}
