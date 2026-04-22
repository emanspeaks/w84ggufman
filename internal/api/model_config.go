package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type hfModelConfigJSON struct {
	NumHiddenLayers   int `json:"num_hidden_layers"`
	NumKeyValueHeads  int `json:"num_key_value_heads"`
	NumAttentionHeads int `json:"num_attention_heads"`
	HiddenSize        int `json:"hidden_size"`
	HeadDim           int `json:"head_dim"`
}

type modelConfigResponse struct {
	Layers    int `json:"layers"`
	KVHeads   int `json:"kvHeads"`
	HeadDim   int `json:"headDim"`
	CtxSize   int `json:"ctxSize"`   // from per-model .w84ggufman.json; 0 = unset
	GlobalCtx int `json:"globalCtx"` // from preset global section; 0 = unset
}

func (s *Server) HandleGetModelConfig(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" || strings.Contains(id, "..") {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var repoDir string
	var hfRepoID string

	if filepath.IsAbs(id) {
		clean := filepath.Clean(id)
		modelsClean := filepath.Clean(s.cfg.ModelsDir)
		if !strings.HasPrefix(clean, modelsClean+string(filepath.Separator)) {
			http.Error(w, "path not under models dir", http.StatusBadRequest)
			return
		}
		repoDir = clean
	} else {
		repoDir = filepath.Join(s.cfg.ModelsDir, filepath.FromSlash(id))
		if strings.Count(id, "/") == 1 {
			hfRepoID = id
		}
	}

	cfg, ok := readLocalConfigJSON(repoDir)
	if !ok && hfRepoID != "" {
		cfg, ok = fetchConfigJSONFromHF(hfRepoID, s.cfg.HFToken)
	}

	layers, kvHeads, headDim := 0, 0, 0
	if ok {
		layers = cfg.NumHiddenLayers
		kvHeads = cfg.NumKeyValueHeads
		if kvHeads == 0 {
			kvHeads = cfg.NumAttentionHeads
		}
		headDim = cfg.HeadDim
		if headDim == 0 && cfg.NumAttentionHeads > 0 {
			headDim = cfg.HiddenSize / cfg.NumAttentionHeads
		}
	}

	meta := s.deps.ReadModelMeta(repoDir)

	globalCtx := 0
	if view, err := s.preset.LoadView(); err == nil {
		if v, exists := view.Global["ctx_size"]; exists {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				globalCtx = n
			}
		}
	}

	writeJSON(w, modelConfigResponse{
		Layers:    layers,
		KVHeads:   kvHeads,
		HeadDim:   headDim,
		CtxSize:   meta.CtxSize,
		GlobalCtx: globalCtx,
	})
}

func (s *Server) HandlePatchModelMeta(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" || strings.Contains(id, "..") {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	var repoDir string
	if filepath.IsAbs(id) {
		clean := filepath.Clean(id)
		modelsClean := filepath.Clean(s.cfg.ModelsDir)
		if !strings.HasPrefix(clean, modelsClean+string(filepath.Separator)) {
			http.Error(w, "path not under models dir", http.StatusBadRequest)
			return
		}
		repoDir = clean
	} else {
		repoDir = filepath.Join(s.cfg.ModelsDir, filepath.FromSlash(id))
	}

	var body struct {
		CtxSize *int `json:"ctxSize"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.CtxSize == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	meta := s.deps.ReadModelMeta(repoDir)
	meta.CtxSize = *body.CtxSize
	if err := s.deps.WriteModelMeta(repoDir, meta); err != nil {
		http.Error(w, "failed to save meta: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readLocalConfigJSON(repoDir string) (hfModelConfigJSON, bool) {
	data, err := os.ReadFile(filepath.Join(repoDir, "config.json"))
	if err != nil {
		return hfModelConfigJSON{}, false
	}
	var cfg hfModelConfigJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		return hfModelConfigJSON{}, false
	}
	return cfg, true
}

func fetchConfigJSONFromHF(repoID, token string) (hfModelConfigJSON, bool) {
	url := "https://huggingface.co/" + repoID + "/resolve/main/config.json"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return hfModelConfigJSON{}, false
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return hfModelConfigJSON{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return hfModelConfigJSON{}, false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return hfModelConfigJSON{}, false
	}
	var cfg hfModelConfigJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		return hfModelConfigJSON{}, false
	}
	return cfg, true
}
