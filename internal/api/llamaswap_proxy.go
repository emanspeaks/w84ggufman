package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// HandleLlamaSwapModels returns the current model list from llama-swap.
//
// llama-swap has no REST GET /api/models/ endpoint; the model list with live
// state is only published through its SSE stream at GET /api/events.  We
// connect to that stream, wait for the first "modelStatus" envelope (which
// llama-swap always emits immediately on connect), parse it, and return the
// inner []Model array as plain JSON.
func (s *Server) HandleLlamaSwapModels(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusServiceUnavailable)
		return
	}
	if s.cfg.LlamaServerURL == "" {
		http.Error(w, "llama-swap server not configured", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.cfg.LlamaServerURL, "/")+"/api/events", nil)
	if err != nil {
		http.Error(w, "failed to build request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "llama-swap unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		http.Error(w, "llama-swap requires an API key — set apiKey in your w84ggufman config", http.StatusServiceUnavailable)
		return
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		http.Error(w, "llama-swap /api/events returned "+resp.Status+": "+string(body), http.StatusBadGateway)
		return
	}

	// SSE envelope: {"type":"modelStatus","data":"[{...}]"}
	// The inner "data" value is a JSON-encoded string containing []Model.
	type sseEnvelope struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var env sseEnvelope
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			continue
		}
		if env.Type != "modelStatus" {
			continue
		}
		// env.Data is a JSON-encoded string; validate it's a JSON array then
		// write it directly.
		if !json.Valid([]byte(env.Data)) {
			http.Error(w, "malformed modelStatus payload from llama-swap", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(env.Data))
		return
	}
	if err := scanner.Err(); err != nil && err != context.DeadlineExceeded {
		http.Error(w, "error reading llama-swap event stream: "+err.Error(), http.StatusBadGateway)
		return
	}
	http.Error(w, "llama-swap event stream closed without modelStatus", http.StatusBadGateway)
}

// HandleLlamaSwapLoadModel triggers a model load by sending a GET request to
// llama-swap's /upstream/{id}/ endpoint.  llama-swap starts the model on the
// first request routed to it; we discard the response body immediately rather
// than waiting for full inference output.
func (s *Server) HandleLlamaSwapLoadModel(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" || strings.Contains(id, "..") {
		http.Error(w, "invalid model id", http.StatusBadRequest)
		return
	}
	// Fire-and-forget GET with a short context so we return quickly after
	// llama-swap acknowledges the request (or starts the process).
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.cfg.LlamaServerURL, "/")+"/upstream/"+id+"/", nil)
	if err != nil {
		http.Error(w, "failed to build request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil && ctx.Err() == nil {
		http.Error(w, "llama-swap unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	if resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	w.WriteHeader(http.StatusAccepted)
}

// HandleLlamaSwapUnloadAll proxies to llama-swap's POST /api/models/unload.
func (s *Server) HandleLlamaSwapUnloadAll(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusServiceUnavailable)
		return
	}
	s.proxyLlamaSwap(w, r, http.MethodPost, "/api/models/unload", r.Body)
}

// HandleLlamaSwapUnloadModel proxies to llama-swap's
// POST /api/models/unload/{id}.
// Note: llama-swap uses a gin wildcard /*model so the id is prefixed with "/".
func (s *Server) HandleLlamaSwapUnloadModel(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusServiceUnavailable)
		return
	}
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
