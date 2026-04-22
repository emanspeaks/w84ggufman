package main

import (
"embed"
"flag"
"fmt"
"io/fs"
"log"
"net/http"
"os"

internalapi "github.com/emanspeaks/w84ggufman/internal/api"
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

log.Printf("w84ggufman %s starting", version)

cfg, err := loadConfig(*configPath)
if err != nil {
log.Fatalf("failed to load config: %v", err)
}

if cfg.LlamaSwapConfig == "" {
	ensureManagedINI(cfg.ModelsDir)
}

pm := newPresetManager(cfg)
lsm := newLlamaSwapManager(cfg)
migrateOldLayout(cfg, pm, lsm)
reorganizeExistingLayout(cfg, pm, lsm)
dl := newDownloader(cfg, pm, lsm)
var llamaSwapDep internalapi.LlamaSwapManager
if lsm != nil {
llamaSwapDep = llamaSwapAdapter{l: lsm}
}
srv := internalapi.NewServer(toAPIConfig(cfg), internalapi.Dependencies{
Downloader:        downloaderAdapter{d: dl},
Preset:            presetAdapter{p: pm},
LlamaSwap:         llamaSwapDep,
DetectVRAMBytes:     detectVRAMBytes,
DetectVRAMUsedBytes: detectVRAMUsedBytes,
RestartService:      restartService,
RemoveAllWritable: removeAllWritable,
IsOrgDir:          isOrgDir,
ScanFilesRelative: scanFilesRelative,
IsIgnoredEntry:    isIgnoredEntry,
EffectiveRootIgnorePattern: func(c internalapi.Config) []string {
return append([]string(nil), c.RootIgnorePatterns...)
},
ReadModelMeta: func(dir string) internalapi.ModelMeta {
return toAPIModelMeta(readModelMeta(dir))
},
WriteModelMeta: func(dir string, meta internalapi.ModelMeta) error {
return writeModelMeta(dir, fromAPIModelMeta(meta))
},
MatchesMmproj:        matchesMmproj,
DetectRepoIDFromGGUF: detectRepoIDFromGGUF,
FetchRepoInfo: func(repoID, token string) (*internalapi.RepoInfo, error) {
info, err := fetchRepoInfo(repoID, token)
if err != nil {
return nil, err
}
return toAPIRepoInfo(info), nil
},
FilterIgnoredRelativeFiles: func(repoDir string, files []string, c internalapi.Config) []string {
cfgCopy := cfg
cfgCopy.ModelsDir = c.ModelsDir
cfgCopy.ShowDotFiles = c.ShowDotFiles
cfgCopy.RootIgnorePatterns = append([]string(nil), c.RootIgnorePatterns...)
return filterIgnoredRelativeFiles(repoDir, files, cfgCopy)
},
Version: version,
})

staticFS, err := fs.Sub(staticFiles, "static")
if err != nil {
log.Fatal(err)
}

mux := buildMux(srv, staticFS)
addr := fmt.Sprintf(":%d", cfg.Port)
log.Printf("w84ggufman %s listening on %s", version, addr)
if err := http.ListenAndServe(addr, logRequests(mux)); err != nil {
log.Fatal(err)
}
}