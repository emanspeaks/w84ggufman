package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
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

	ensureManagedINI(cfg.ModelsDir)

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

// ensureManagedINI creates modelsDir/managed.ini with a header comment if the
// file does not already exist. llama-server requires the file to be present
// when started with --models-preset, even when no models are configured yet.
func ensureManagedINI(modelsDir string) {
	path := filepath.Join(modelsDir, "managed.ini")
	if _, err := os.Stat(path); err == nil {
		return
	}
	const header = "; managed by gguf-manager\n; do not edit manually\n"
	if err := os.WriteFile(path, []byte(header), 0664); err != nil {
		log.Printf("warning: could not create managed.ini: %v", err)
	}
}
