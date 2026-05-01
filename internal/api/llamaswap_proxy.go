package api

import (
	"io"
	"net/http"
	"strings"
)

// HandleLlamaSwapModels proxies GET /api/llamaswap/models to the llama-swap
// server's /api/models/ endpoint, which returns JSON with each model's id,
// name, description, state, unlisted flag, aliases, and peerID.
func (s *Server) HandleLlamaSwapModels(w http.ResponseWriter, r *http.Request) {
	s.proxyLlamaSwap(w, r, http.MethodGet, "/api/models/", nil)
}

// HandleLlamaSwapLoadModel triggers a model load by sending a lightweight GET
// request to llama-swap's /upstream/{id}/ endpoint (which causes llama-swap
// to start the model if not already running).
func (s *Server) HandleLlamaSwapLoadModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.Contains(id, "..") {
		http.Error(w, "invalid model id", http.StatusBadRequest)
		return
	}
	// llama-swap starts the model on the first request; we use HEAD so we don't
	// wait for a full inference response.
	s.proxyLlamaSwap(w, r, http.MethodHead, "/upstream/"+id+"/", nil)
}

// HandleLlamaSwapUnloadAll proxies to llama-swap's POST /api/models/unload.
func (s *Server) HandleLlamaSwapUnloadAll(w http.ResponseWriter, r *http.Request) {
	s.proxyLlamaSwap(w, r, http.MethodPost, "/api/models/unload", r.Body)
}

// HandleLlamaSwapUnloadModel proxies to llama-swap's
// POST /api/models/unload/{id}.
func (s *Server) HandleLlamaSwapUnloadModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.Contains(id, "..") {
		http.Error(w, "invalid model id", http.StatusBadRequest)
		return
	}
	s.proxyLlamaSwap(w, r, http.MethodPost, "/api/models/unload/"+id, r.Body)
}

// proxyLlamaSwap forwards a request to the configured llama-swap server and
// copies the response back to the client.
func (s *Server) proxyLlamaSwap(w http.ResponseWriter, _ *http.Request, method, path string, body io.Reader) {
	if s.cfg.LlamaServerURL == "" {
		http.Error(w, "llama-swap server not configured", http.StatusServiceUnavailable)
		return
	}

	url := strings.TrimRight(s.cfg.LlamaServerURL, "/") + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		http.Error(w, "failed to build request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "llama-swap unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy upstream headers that are relevant.
	for _, key := range []string{"Content-Type", "Content-Length"} {
		if v := resp.Header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
