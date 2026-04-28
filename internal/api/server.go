package api

import (
	"encoding/json"
	"net/http"
	"sync"
)

type Config struct {
	ModelsDir               string
	LlamaServerURL          string
	LlamaService            string
	Port                    int
	HFToken                 string
	WarnDownloadGiB         float64
	RamGiB                  float64
	WarnRamPercent          float64
	SelfService             string
	AtopwebURL              string
	LlamaServerLandingPage  string
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
	HasUpdate    bool       `json:"hasUpdate,omitempty"`
	RepoID       string     `json:"repoId,omitempty"`
}

type PresetView struct {
	Global   map[string]string
	Sections map[string]map[string]string
}

type LlamaSwapModelEntry struct {
	Name            string
	ModelPath       string
	ReferencedPaths []string
	Groups          []string
}

type QueueEntry struct {
	ID         int64    `json:"id"`
	Label      string   `json:"label"`
	TotalBytes int64    `json:"totalBytes"`
	RepoID     string   `json:"repoId"`
	Filenames  []string `json:"filenames"`
}

type Downloader interface {
	ActiveInfo() (string, bool)
	CancelDownload()
	// Start enqueues or immediately begins a download. Returns (queued=true) if
	// the request was added to the pending queue rather than started right away.
	Start(repoID string, filenames []string, sidecarFiles []string, totalBytes int64, force bool) (bool, error)
	StreamSSE(w http.ResponseWriter, r *http.Request)
	QueueEntries() []QueueEntry
	RemoveFromQueue(id int64) bool
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
	AddModel(name, modelPath, mmprojPath, vaePath, modelType string) error
	HasModel(name string) (bool, error)
	ReadRaw(name string) (string, error)
	WriteRaw(name, body string) error
	ReadW84Config() (string, error)
	WriteW84Config(body string) error
	ReadAll() (string, error)
	WriteAll(body string) error
}

type Dependencies struct {
	Downloader Downloader
	Preset     PresetManager
	LlamaSwap  LlamaSwapManager

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
	HasUpdateAvailable         func(dir string) bool
	PendingUpdateCount         func() int
	Version                    string
}

type Server struct {
	cfg           Config
	dl            Downloader
	preset        PresetManager
	llamaSwap     LlamaSwapManager
	deps          Dependencies
	editorStateMu sync.Mutex
}

func NewServer(cfg Config, deps Dependencies) *Server {
	return &Server{cfg: cfg, dl: deps.Downloader, preset: deps.Preset, llamaSwap: deps.LlamaSwap, deps: deps}
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
