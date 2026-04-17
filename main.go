package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
)

//go:embed static
var staticFiles embed.FS

// version is injected at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	configPath := flag.String("config", "", "path to JSONC config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	dl := newDownloader(cfg)
	srv := newServer(cfg, dl)

	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("GET /api/local", srv.handleLocal)
	mux.HandleFunc("GET /api/repo", srv.handleRepo)
	mux.HandleFunc("POST /api/download", srv.handleDownload)
	mux.HandleFunc("GET /api/download/status", srv.handleDownloadStatus)
	mux.HandleFunc("DELETE /api/local/{name}", srv.handleDeleteLocal)
	mux.HandleFunc("GET /api/status", srv.handleStatus)
	mux.HandleFunc("POST /api/restart", srv.handleRestart)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("gguf-manager %s listening on %s", version, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
