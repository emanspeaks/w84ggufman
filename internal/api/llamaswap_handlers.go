package api

import (
	"io"
	"net/http"
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

func (s *Server) HandleGetLlamaSwapTemplates(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	writeJSON(w, s.llamaSwap.LoadTemplates())
}

func (s *Server) HandlePutLlamaSwapTemplates(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusNotFound)
		return
	}
	if err := s.llamaSwap.UpdateTemplatesFromJSON(io.LimitReader(r.Body, 64<<10)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
