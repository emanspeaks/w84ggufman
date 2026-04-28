package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const editorStateCacheFile = ".w84editor-state.json"

type editorViewState struct {
	LineNumber int     `json:"lineNumber"`
	Column     int     `json:"column"`
	ScrollTop  float64 `json:"scrollTop"`
	ScrollLeft float64 `json:"scrollLeft"`
}

type editorStatePayload struct {
	Endpoint string          `json:"endpoint"`
	State    editorViewState `json:"state"`
}

func (s *Server) editorStatePath() string {
	return filepath.Join(s.cfg.ModelsDir, editorStateCacheFile)
}

func (s *Server) readEditorStateCache() (map[string]editorViewState, error) {
	path := s.editorStatePath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]editorViewState{}, nil
		}
		return nil, err
	}
	var cache map[string]editorViewState
	if err := json.Unmarshal(b, &cache); err != nil {
		return map[string]editorViewState{}, nil
	}
	if cache == nil {
		cache = map[string]editorViewState{}
	}
	return cache, nil
}

func (s *Server) writeEditorStateCache(cache map[string]editorViewState) error {
	path := s.editorStatePath()
	tmp := path + ".tmp"
	b, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0664); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func validEditorStateEndpoint(endpoint string) bool {
	if endpoint == "" || len(endpoint) > 256 {
		return false
	}
	if strings.Contains(endpoint, "..") {
		return false
	}
	return strings.HasPrefix(endpoint, "/api/")
}

func sanitizeEditorViewState(in editorViewState) editorViewState {
	if in.LineNumber < 1 {
		in.LineNumber = 1
	}
	if in.Column < 1 {
		in.Column = 1
	}
	if in.ScrollTop < 0 {
		in.ScrollTop = 0
	}
	if in.ScrollLeft < 0 {
		in.ScrollLeft = 0
	}
	return in
}

func (s *Server) HandleGetEditorState(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimSpace(r.URL.Query().Get("endpoint"))
	if !validEditorStateEndpoint(endpoint) {
		http.Error(w, "invalid endpoint", http.StatusBadRequest)
		return
	}

	s.editorStateMu.Lock()
	defer s.editorStateMu.Unlock()

	cache, err := s.readEditorStateCache()
	if err != nil {
		http.Error(w, "failed to read editor state", http.StatusInternalServerError)
		return
	}

	state, ok := cache[endpoint]
	if !ok {
		writeJSON(w, map[string]any{"found": false})
		return
	}
	writeJSON(w, map[string]any{"found": true, "state": state})
}

func (s *Server) HandlePutEditorState(w http.ResponseWriter, r *http.Request) {
	var req editorStatePayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Endpoint = strings.TrimSpace(req.Endpoint)
	if !validEditorStateEndpoint(req.Endpoint) {
		http.Error(w, "invalid endpoint", http.StatusBadRequest)
		return
	}
	req.State = sanitizeEditorViewState(req.State)

	s.editorStateMu.Lock()
	defer s.editorStateMu.Unlock()

	cache, err := s.readEditorStateCache()
	if err != nil {
		http.Error(w, "failed to read editor state", http.StatusInternalServerError)
		return
	}
	cache[req.Endpoint] = req.State
	if err := s.writeEditorStateCache(cache); err != nil {
		http.Error(w, "failed to write editor state", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
