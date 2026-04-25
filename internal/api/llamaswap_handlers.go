package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

func (s *Server) HandleGetLlamaSwapRaw(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	body, err := s.llamaSwap.ReadRaw(name)
	if err != nil {
		http.Error(w, "failed to read config.yaml: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body))
}

func (s *Server) HandlePutLlamaSwapRaw(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.llamaSwap.WriteRaw(name, strings.TrimRight(string(raw), "\r\n")); err != nil {
		http.Error(w, "failed to write config.yaml: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) HandleGetW84Config(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	body, err := s.llamaSwap.ReadW84Config()
	if err != nil {
		http.Error(w, "failed to read w84 config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body))
}

func (s *Server) HandlePutW84Config(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.llamaSwap.WriteW84Config(string(raw)); err != nil {
		http.Error(w, "failed to write w84 config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) HandleGetLlamaSwapConfig(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	body, err := s.llamaSwap.ReadAll()
	if err != nil {
		http.Error(w, "failed to read config.yaml: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body))
}

func (s *Server) HandlePutLlamaSwapConfig(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.llamaSwap.WriteAll(string(raw)); err != nil {
		http.Error(w, "failed to write config.yaml: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type addLlamaSwapModelRequest struct {
	RepoID     string `json:"repoId"`
	Filename   string `json:"filename"`
	MmprojFile string `json:"mmprojFile,omitempty"`
	VaeFile    string `json:"vaeFile,omitempty"`
	ModelType  string `json:"modelType"`
}

type addLlamaSwapModelResponse struct {
	Name       string `json:"name"`
	EntryBlock string `json:"entryBlock"`
	ModelType  string `json:"modelType"`
}

func buildEntryBlock(name, rawBody string) string {
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(name)
	b.WriteString(":\n")
	for _, line := range strings.Split(rawBody, "\n") {
		if strings.TrimSpace(line) != "" {
			b.WriteString("    ")
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func deriveModelName(filename string) string {
	base := filepath.Base(filename)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return base
}

func (s *Server) HandleAddLlamaSwapModel(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	var req addLlamaSwapModelRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.RepoID == "" || req.Filename == "" {
		http.Error(w, "repoId and filename are required", http.StatusBadRequest)
		return
	}
	if strings.Contains(req.RepoID, "..") || strings.Contains(req.Filename, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if req.ModelType != "" && req.ModelType != "llm" && req.ModelType != "sd" {
		http.Error(w, "modelType must be 'llm' or 'sd'", http.StatusBadRequest)
		return
	}

	repoDir := filepath.Join(s.cfg.ModelsDir, filepath.FromSlash(req.RepoID))
	modelPath := filepath.Join(repoDir, filepath.FromSlash(req.Filename))
	name := deriveModelName(req.Filename)
	if name == "" {
		http.Error(w, "could not derive model name from filename", http.StatusBadRequest)
		return
	}

	var mmprojPath, vaePath string
	if req.MmprojFile != "" {
		mmprojPath = filepath.Join(repoDir, filepath.FromSlash(req.MmprojFile))
	}
	if req.VaeFile != "" {
		vaePath = filepath.Join(repoDir, filepath.FromSlash(req.VaeFile))
	}

	if err := s.llamaSwap.AddModel(name, modelPath, mmprojPath, vaePath, req.ModelType); err != nil {
		http.Error(w, "failed to add model: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rawBody, _ := s.llamaSwap.ReadRaw(name)
	modelType := req.ModelType
	if modelType == "" {
		modelType = "llm"
	}
	writeJSON(w, addLlamaSwapModelResponse{
		Name:       name,
		EntryBlock: buildEntryBlock(name, rawBody),
		ModelType:  modelType,
	})
}
