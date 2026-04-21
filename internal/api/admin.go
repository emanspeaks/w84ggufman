package api

import (
	"log"
	"net/http"
	"time"
)

func (s *Server) HandleRestart(w http.ResponseWriter, r *http.Request) {
	log.Printf("restarting service %s", s.cfg.LlamaService)
	if err := s.deps.RestartService(s.cfg.LlamaService); err != nil {
		log.Printf("error: restart %s: %v", s.cfg.LlamaService, err)
		http.Error(w, "failed to restart service: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("service %s restarted", s.cfg.LlamaService)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) HandleRestartSelf(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SelfService == "" {
		http.Error(w, "selfService not configured", http.StatusNotImplemented)
		return
	}
	log.Printf("restarting self service %s", s.cfg.SelfService)
	w.WriteHeader(http.StatusAccepted)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := s.deps.RestartService(s.cfg.SelfService); err != nil {
			log.Printf("error: restart self %s: %v", s.cfg.SelfService, err)
		}
	}()
}
