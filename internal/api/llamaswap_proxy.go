package api

import (
	"bufio"
	"bytes"
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
	//
	// llama-swap emits large logData events (proxy + upstream log history) before
	// the modelStatus event.  We use ReadSlice, which reuses the internal buffer
	// without allocating, to identify each line type.  Only when we spot a
	// modelStatus line do we copy bytes into a real allocation.
	type sseEnvelope struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}

	const modelStatusMark = `"modelStatus"`
	const dataPrefix = "data:"
	reader := bufio.NewReaderSize(resp.Body, 4096)

	// drainLine discards the rest of the current line without allocating.
	drainLine := func() error {
		for {
			_, err := reader.ReadSlice('\n')
			if err != bufio.ErrBufferFull {
				return err
			}
		}
	}

	for {
		// ReadSlice returns a slice of the internal buffer — zero allocation.
		// If the line is longer than the buffer, err == io.ErrBufferFull.
		head, readErr := reader.ReadSlice('\n')
		isFullLine := readErr != bufio.ErrBufferFull

		isDataLine := bytes.HasPrefix(bytes.TrimSpace(head), []byte(dataPrefix))
		looksLikeModelStatus := isDataLine && bytes.Contains(head, []byte(modelStatusMark))

		if !looksLikeModelStatus {
			if !isFullLine {
				// Long non-modelStatus line (e.g. logData history): drain without allocating.
				if drainErr := drainLine(); drainErr != nil {
					if drainErr != io.EOF {
						http.Error(w, "error reading llama-swap event stream: "+drainErr.Error(), http.StatusBadGateway)
						return
					}
					break
				}
				continue
			}
			if readErr != nil { // EOF at end of a complete line
				break
			}
			continue
		}

		// Potential modelStatus line — copy head before the next ReadSlice call
		// invalidates the buffer, then read any remaining bytes if the line was cut.
		lineBytes := append([]byte{}, head...)
		if !isFullLine {
			rest, restErr := reader.ReadString('\n')
			lineBytes = append(lineBytes, rest...)
			if restErr != nil && restErr != io.EOF {
				http.Error(w, "error reading llama-swap event stream: "+restErr.Error(), http.StatusBadGateway)
				return
			}
		}

		payload := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimRight(lineBytes, "\r\n"), []byte(dataPrefix)))
		var env sseEnvelope
		if jsonErr := json.Unmarshal(payload, &env); jsonErr != nil || env.Type != "modelStatus" {
			if readErr != nil {
				break
			}
			continue
		}
		if !json.Valid([]byte(env.Data)) {
			http.Error(w, "malformed modelStatus payload from llama-swap", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(env.Data))
		return
	}

	if ctx.Err() != nil {
		http.Error(w, "timed out waiting for modelStatus from llama-swap", http.StatusBadGateway)
	} else {
		http.Error(w, "llama-swap event stream closed without modelStatus", http.StatusBadGateway)
	}
}

// HandleLlamaSwapModelsStream opens a persistent SSE stream that forwards only
// llama-swap modelStatus updates as SSE `event: modelStatus` messages.
func (s *Server) HandleLlamaSwapModelsStream(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusServiceUnavailable)
		return
	}
	if s.cfg.LlamaServerURL == "" {
		http.Error(w, "llama-swap server not configured", http.StatusServiceUnavailable)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		strings.TrimRight(s.cfg.LlamaServerURL, "/")+"/api/events", nil)
	if err != nil {
		http.Error(w, "failed to build request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	type sseEnvelope struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}

	const modelStatusMark = `"modelStatus"`
	const dataPrefix = "data:"
	reader := bufio.NewReaderSize(resp.Body, 4096)

	// keepalive comment so proxies open the stream immediately
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	drainLine := func() error {
		for {
			_, err := reader.ReadSlice('\n')
			if err != bufio.ErrBufferFull {
				return err
			}
		}
	}

	for {
		head, readErr := reader.ReadSlice('\n')
		isFullLine := readErr != bufio.ErrBufferFull

		isDataLine := bytes.HasPrefix(bytes.TrimSpace(head), []byte(dataPrefix))
		looksLikeModelStatus := isDataLine && bytes.Contains(head, []byte(modelStatusMark))

		if !looksLikeModelStatus {
			if !isFullLine {
				if drainErr := drainLine(); drainErr != nil {
					return
				}
				continue
			}
			if readErr != nil {
				return
			}
			continue
		}

		lineBytes := append([]byte{}, head...)
		if !isFullLine {
			rest, restErr := reader.ReadString('\n')
			lineBytes = append(lineBytes, rest...)
			if restErr != nil && restErr != io.EOF {
				return
			}
		}

		payload := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimRight(lineBytes, "\r\n"), []byte(dataPrefix)))
		var env sseEnvelope
		if jsonErr := json.Unmarshal(payload, &env); jsonErr != nil || env.Type != "modelStatus" {
			if readErr != nil {
				return
			}
			continue
		}
		if !json.Valid([]byte(env.Data)) {
			continue
		}

		if _, werr := io.WriteString(w, "event: modelStatus\n"); werr != nil {
			return
		}
		if _, werr := io.WriteString(w, "data: "+env.Data+"\n\n"); werr != nil {
			return
		}
		flusher.Flush()

		if readErr != nil {
			return
		}
	}
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
	// llama-swap starts the model on the first request to /upstream/{id}/ and
	// proxies it all the way through to the running model before responding.
	// That can take many seconds while the model loads.  Fire it in a goroutine
	// so we return 202 to the browser immediately and let polling track state.
	url := strings.TrimRight(s.cfg.LlamaServerURL, "/") + "/upstream/" + id + "/"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		client := &http.Client{Timeout: 25 * time.Second}
		resp, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
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
func (s *Server) proxyLlamaSwap(w http.ResponseWriter, r *http.Request, method, path string, body io.Reader) {
	if s.cfg.LlamaServerURL == "" {
		http.Error(w, "llama-swap server not configured", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	url := strings.TrimRight(s.cfg.LlamaServerURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		http.Error(w, "failed to build request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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

// HandleLlamaSwapSettings returns settings from the w84 config that the
// frontend needs at startup (e.g. the log-pane history line count).
func (s *Server) HandleLlamaSwapSettings(w http.ResponseWriter, r *http.Request) {
	logLines := 500
	if s.llamaSwap != nil {
		logLines = s.llamaSwap.PresetLogLines()
	}
	writeJSON(w, map[string]any{"presetLogLines": logLines})
}

// HandleLlamaSwapLogStream proxies a live log stream from llama-swap.
// The optional {id} path value maps to llama-swap's /logs/stream/*logMonitorID.
// Model IDs with slashes (e.g. "author/model") are supported via the {id...}
// multi-segment wildcard in the router.
// The upstream ?no-history query param is forwarded as-is.
func (s *Server) HandleLlamaSwapLogStream(w http.ResponseWriter, r *http.Request) {
	if s.llamaSwap == nil {
		http.Error(w, "llama-swap not configured", http.StatusServiceUnavailable)
		return
	}
	if s.cfg.LlamaServerURL == "" {
		http.Error(w, "llama-swap server not configured", http.StatusServiceUnavailable)
		return
	}

	path := "/logs/stream"
	if id := r.PathValue("id"); id != "" {
		path += "/" + id
	}
	if q := r.URL.RawQuery; q != "" {
		path += "?" + q
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		strings.TrimRight(s.cfg.LlamaServerURL, "/")+path, nil)
	if err != nil {
		http.Error(w, "failed to build request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Use a transport with no response timeout so the stream stays open.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "llama-swap unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		http.Error(w, "llama-swap /logs/stream returned "+resp.Status+": "+string(body), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // prevent nginx buffering
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// ensure time is used (imported for HandleLlamaSwapModels timeout)
var _ = time.Second
