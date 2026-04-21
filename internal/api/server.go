package api

import (
	"encoding/json"
	"io"
	"net/http"
)

type Config struct {
	ModelsDir               string
	LlamaServerURL          string
	LlamaService            string
	Port                    int
	HFToken                 string
	WarnDownloadGiB         float64
	VramGiB                 float64
	WarnVramPercent         float64
	SelfService             string
	ForceRestartOnLlamaSwap bool
	ShowDotFiles            bool
	RootIgnorePatterns      []string
}

type ModelMeta struct {
	RepoID     string
	SkipHFSync bool
	Ignore     []string
}

type RepoFile struct {
	Filename    string `json:"filename"`
	Size        *int64 `json:"size"`
	DownloadURL string `json:"downloadURL"`
	DisplayName string `json:"displayName,omitempty"`
}

type RepoInfo struct {
	IsVision     bool       `json:"isVision"`
	Models       []RepoFile `json:"models"`
	Sidecars     []RepoFile `json:"sidecars"`
	PipelineTag  string     `json:"pipelineTag,omitempty"`
	Tags         []string   `json:"tags,omitempty"`
	PresentFiles []string   `json:"presentFiles"`
	RogueFiles   []string   `json:"rogueFiles,omitempty"`
	LocalOnly    bool       `json:"localOnly,omitempty"`
}

type PresetView struct {
	Global   map[string]string
	Sections map[string]map[string]string
}

type LlamaSwapModelEntry struct {
	Name            string
	ModelPath       string
	ReferencedPaths []string
}

type Downloader interface {
	ActiveInfo() (string, bool)
	CancelDownload()
	Start(repoID string, filenames []string, sidecarFiles []string, totalBytes int64, force bool) error
	StreamSSE(w http.ResponseWriter, r *http.Request)
}

type PresetManager interface {
	LoadView() (PresetView, error)
	RemoveModel(name string) error
	UpdateGlobal(kvs map[string]string) error
	UpsertModelKeys(name string, kvs map[string]string) error
	ReadRaw(name string) (string, error)
	WriteRaw(name, body string) error
	ReadAll() (string, error)
	WriteAll(body string) error
}

type LlamaSwapManager interface {
	ListModels() ([]LlamaSwapModelEntry, error)
	RemoveModel(name string) error
	LoadTemplates() any
	ReadRaw(name string) (string, error)
	WriteRaw(name, body string) error
	UpdateTemplatesFromJSON(r io.Reader) error
	ReadAll() (string, error)
	WriteAll(body string) error
}

type Dependencies struct {
	Downloader Downloader
	Preset     PresetManager
	LlamaSwap  LlamaSwapManager

	DetectVRAMBytes            func() uint64
	RestartService             func(service string) error
	RemoveAllWritable          func(path string) error
	IsOrgDir                   func(dir string) bool
	ScanFilesRelative          func(dir string) ([]string, int64)
	IsIgnoredEntry             func(name string, patterns []string, showDotFiles bool, isDir bool) bool
	EffectiveRootIgnorePattern func(cfg Config) []string
	ReadModelMeta              func(dir string) ModelMeta
	WriteModelMeta             func(dir string, meta ModelMeta) error
	MatchesMmproj              func(filename string) bool
	DetectRepoIDFromGGUF       func(dir string, ggufFiles []string) string
	FetchRepoInfo              func(repoID, token string) (*RepoInfo, error)
	FilterIgnoredRelativeFiles func(repoDir string, files []string, cfg Config) []string
	Version                    string
}

type Server struct {
	cfg       Config
	dl        Downloader
	preset    PresetManager
	llamaSwap LlamaSwapManager
	deps      Dependencies
	vramBytes uint64
}

func NewServer(cfg Config, deps Dependencies) *Server {
	vram := uint64(cfg.VramGiB * 1024 * 1024 * 1024)
	if vram == 0 && deps.DetectVRAMBytes != nil {
		vram = deps.DetectVRAMBytes()
	}
	return &Server{cfg: cfg, dl: deps.Downloader, preset: deps.Preset, llamaSwap: deps.LlamaSwap, deps: deps, vramBytes: vram}
}

type diskInfo struct {
	TotalBytes uint64 `json:"totalBytes"`
	FreeBytes  uint64 `json:"freeBytes"`
	UsedBytes  uint64 `json:"usedBytes"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
