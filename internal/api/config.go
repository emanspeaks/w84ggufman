package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (s *Server) HandleGetPreset(w http.ResponseWriter, r *http.Request) {
	f, err := s.preset.LoadView()
	if err != nil {
		http.Error(w, "failed to load preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	type presetResponse struct {
		Global   map[string]string            `json:"global"`
		Sections map[string]map[string]string `json:"sections"`
	}
	writeJSON(w, presetResponse{Global: f.Global, Sections: f.Sections})
}

func (s *Server) HandleUpdatePresetGlobal(w http.ResponseWriter, r *http.Request) {
	var kvs map[string]string
	if err := json.NewDecoder(r.Body).Decode(&kvs); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.preset.UpdateGlobal(kvs); err != nil {
		http.Error(w, "failed to update preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) HandleUpdatePresetModel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	var kvs map[string]string
	if err := json.NewDecoder(r.Body).Decode(&kvs); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.preset.UpsertModelKeys(name, kvs); err != nil {
		http.Error(w, "failed to update preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) HandleGetPresetRaw(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		http.Error(w, "invalid model name", http.StatusBadRequest)
		return
	}
	body, err := s.preset.ReadRaw(name)
	if err != nil {
		http.Error(w, "failed to read preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body))
}

func (s *Server) HandleUpdatePresetRaw(w http.ResponseWriter, r *http.Request) {
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
	body := strings.TrimRight(string(raw), "\r\n")
	if err := s.preset.WriteRaw(name, body); err != nil {
		http.Error(w, "failed to write preset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) HandleGetPresetConfig(w http.ResponseWriter, r *http.Request) {
	body, err := s.preset.ReadAll()
	if err != nil {
		http.Error(w, "failed to read models.ini: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(body))
}

func (s *Server) HandlePutPresetConfig(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.preset.WriteAll(string(raw)); err != nil {
		http.Error(w, "failed to write models.ini: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
