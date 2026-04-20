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
	"regexp"
	"sort"
	"strings"
	"time"
)

//go:embed static
var staticFiles embed.FS

// version is injected at build time via -ldflags "-X main.version=..."
var version = "dev"

// verbose controls whether high-frequency polling endpoints (e.g. GET /api/status)
// are included in the request log.
var verbose bool

func main() {
	configPath := flag.String("config", "", "path to JSONC config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	verboseFlag := flag.Bool("verbose", false, "log all API requests including polling endpoints")
	flag.Parse()
	verbose = *verboseFlag

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	ensureManagedINI(cfg.ModelsDir)

	pm := newPresetManager(cfg)
	lsm := newLlamaSwapManager(cfg)
	dl := newDownloader(cfg, pm, lsm)
	srv := newServer(cfg, dl, pm, lsm)

	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("GET /api/local", srv.handleLocal)
	mux.HandleFunc("GET /api/repo", srv.handleRepo)
	mux.HandleFunc("GET /api/readme", srv.handleReadme)
	mux.HandleFunc("POST /api/download", srv.handleDownload)
	mux.HandleFunc("POST /api/download/cancel", srv.handleCancelDownload)
	mux.HandleFunc("GET /api/download/status", srv.handleDownloadStatus)
	mux.HandleFunc("DELETE /api/local/{name}", srv.handleDeleteLocal)
	mux.HandleFunc("GET /api/status", srv.handleStatus)
	mux.HandleFunc("POST /api/restart", srv.handleRestart)
	mux.HandleFunc("POST /api/restart-self", srv.handleRestartSelf)
	mux.HandleFunc("GET /api/preset", srv.handleGetPreset)
	mux.HandleFunc("POST /api/preset/global", srv.handleUpdatePresetGlobal)
	mux.HandleFunc("GET /api/preset/raw/{name}", srv.handleGetPresetRaw)
	mux.HandleFunc("PUT /api/preset/raw/{name}", srv.handleUpdatePresetRaw)
	mux.HandleFunc("POST /api/preset/{name}", srv.handleUpdatePresetModel)
	mux.HandleFunc("GET /api/preset/config", srv.handleGetPresetConfig)
	mux.HandleFunc("PUT /api/preset/config", srv.handlePutPresetConfig)
	mux.HandleFunc("GET /api/llamaswap/raw/{name}", srv.handleGetLlamaSwapRaw)
	mux.HandleFunc("PUT /api/llamaswap/raw/{name}", srv.handlePutLlamaSwapRaw)
	mux.HandleFunc("GET /api/llamaswap/config", srv.handleGetLlamaSwapConfig)
	mux.HandleFunc("PUT /api/llamaswap/config", srv.handlePutLlamaSwapConfig)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("w84ggufman %s listening on %s", version, addr)
	if err := http.ListenAndServe(addr, logRequests(mux)); err != nil {
		log.Fatal(err)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying ResponseWriter so SSE streaming works
// through the logging middleware.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logRequests logs every /api/ request to the system logger with method, path,
// status code, and elapsed time. High-frequency polling endpoints like
// GET /api/status are suppressed unless --verbose is set.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		quiet := !verbose && r.Method == http.MethodGet && r.URL.Path == "/api/status"
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		if !quiet {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

var shardRe = regexp.MustCompile(`-\d{5}-of-\d{5}\.gguf$`)

// findFirstShard returns the path to the first shard file (-00001-of-) among
// files, falling back to the first file alphabetically. files must be basenames;
// dir is the containing directory.
func findFirstShard(dir string, files []string) string {
	for _, f := range files {
		if strings.Contains(f, "-00001-of-") {
			return filepath.Join(dir, f)
		}
	}
	if len(files) > 0 {
		return filepath.Join(dir, files[0])
	}
	return dir
}

// ensureManagedINI creates modelsDir/models.ini if it does not already exist,
// pre-populated with entries for any model directories already on disk.
// llama-server requires the file to be present when started with --models-preset.
func ensureManagedINI(modelsDir string) {
	path := filepath.Join(modelsDir, "models.ini")
	if _, err := os.Stat(path); err == nil {
		return
	}

	var sb strings.Builder
	sb.WriteString("; managed by w84ggufman — manual edits are preserved\n")

	type modelEntry struct {
		name, modelPath, mmprojPath string
	}
	var entries []modelEntry

	if dirs, err := os.ReadDir(modelsDir); err == nil {
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			parentDir := filepath.Join(modelsDir, d.Name())
			parentFiles, err := os.ReadDir(parentDir)
			if err != nil {
				continue
			}

			// Collect gguf files at the parent level and detect quant subdirs.
			var topModelFiles, topMmprojFiles []string
			var quantDirs []string
			for _, f := range parentFiles {
				if f.IsDir() {
					quantDir := filepath.Join(parentDir, f.Name())
					subFiles, _ := os.ReadDir(quantDir)
					for _, sf := range subFiles {
						if !sf.IsDir() && strings.HasSuffix(sf.Name(), ".gguf") &&
							!strings.HasPrefix(strings.ToLower(sf.Name()), "mmproj-") {
							quantDirs = append(quantDirs, f.Name())
							break
						}
					}
				} else if strings.HasSuffix(f.Name(), ".gguf") {
					name := strings.ToLower(f.Name())
					if strings.HasPrefix(name, "mmproj-") {
						topMmprojFiles = append(topMmprojFiles, f.Name())
					} else {
						topModelFiles = append(topModelFiles, f.Name())
					}
				}
			}

			// Choose mmproj from parent level (prefer F16).
			pickMmproj := func(files []string, dir string) string {
				if len(files) == 0 {
					return ""
				}
				chosen := files[0]
				for _, f := range files {
					if strings.Contains(strings.ToLower(f), "f16") {
						chosen = f
						break
					}
				}
				return filepath.Join(dir, chosen)
			}

			if len(quantDirs) > 0 {
				// New nested layout: each subdir is a quant.
				mmprojPath := pickMmproj(topMmprojFiles, parentDir)
				for _, qName := range quantDirs {
					quantDir := filepath.Join(parentDir, qName)
					subFiles, _ := os.ReadDir(quantDir)
					var modelFiles []string
					for _, sf := range subFiles {
						if !sf.IsDir() && strings.HasSuffix(sf.Name(), ".gguf") &&
							!strings.HasPrefix(strings.ToLower(sf.Name()), "mmproj-") {
							modelFiles = append(modelFiles, sf.Name())
						}
					}
					if len(modelFiles) == 0 {
						continue
					}
					modelPath := findFirstShard(quantDir, modelFiles)
					// Section name derived from the model filename for uniqueness across repos.
					sectionName := modelNameFromFilename(filepath.Base(modelPath))
					entries = append(entries, modelEntry{sectionName, modelPath, mmprojPath})
				}
			} else if len(topModelFiles) > 0 {
				// Old flat layout: model files sit directly in this directory.
				mmprojPath := pickMmproj(topMmprojFiles, parentDir)

				// Check whether the files are shards of one quant or separate quants.
				allShards := true
				for _, f := range topModelFiles {
					if !shardRe.MatchString(f) {
						allShards = false
						break
					}
				}

				if len(topModelFiles) > 1 && !allShards {
					for _, f := range topModelFiles {
						quantName := strings.TrimSuffix(f, ".gguf")
						entries = append(entries, modelEntry{quantName, filepath.Join(parentDir, f), mmprojPath})
					}
				} else {
					modelPath := findFirstShard(parentDir, topModelFiles)
					entries = append(entries, modelEntry{d.Name(), modelPath, mmprojPath})
				}
			}
		}
	}

	if len(entries) > 0 {
		sb.WriteString("\n[*]\nctx-size = 65536\nflash-attn = on\njinja = true\nn-gpu-layers = 999\n")
		sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
		for _, e := range entries {
			sb.WriteString("\n[" + e.name + "]\n")
			sb.WriteString("model = " + e.modelPath + "\n")
			if e.mmprojPath != "" {
				sb.WriteString("mmproj = " + e.mmprojPath + "\n")
			}
		}
	}

	if err := os.WriteFile(path, []byte(sb.String()), 0664); err != nil {
		log.Printf("warning: could not create models.ini: %v", err)
	} else if len(entries) > 0 {
		log.Printf("created models.ini with %d existing model(s)", len(entries))
	}
}
