package main

import (
	"io/fs"
	"net/http"

	internalapi "github.com/emanspeaks/w84ggufman/internal/api"
)

func buildMux(srv *internalapi.Server, staticFS fs.FS) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("GET /api/local", srv.HandleLocal)
	mux.HandleFunc("GET /api/repo", srv.HandleRepo)
	mux.HandleFunc("GET /api/readme", srv.HandleReadme)
	mux.HandleFunc("POST /api/download", srv.HandleDownload)
	mux.HandleFunc("POST /api/download/cancel", srv.HandleCancelDownload)
	mux.HandleFunc("DELETE /api/queue/{id}", srv.HandleRemoveFromQueue)
	mux.HandleFunc("GET /api/download/status", srv.HandleDownloadStatus)
	mux.HandleFunc("DELETE /api/local/{name}", srv.HandleDeleteLocal)
	mux.HandleFunc("DELETE /api/local", srv.HandleDeleteRepo)
	mux.HandleFunc("POST /api/local/delete-files", srv.HandleDeleteFiles)
	mux.HandleFunc("GET /api/local-files", srv.HandleLocalFiles)
	mux.HandleFunc("GET /api/status", srv.HandleStatus)
	mux.HandleFunc("GET /api/disk-usage", srv.HandleDiskUsage)
	mux.HandleFunc("POST /api/restart", srv.HandleRestart)
	mux.HandleFunc("POST /api/restart-self", srv.HandleRestartSelf)
	mux.HandleFunc("GET /api/preset", srv.HandleGetPreset)
	mux.HandleFunc("POST /api/preset/global", srv.HandleUpdatePresetGlobal)
	mux.HandleFunc("GET /api/preset/raw/{name}", srv.HandleGetPresetRaw)
	mux.HandleFunc("PUT /api/preset/raw/{name}", srv.HandleUpdatePresetRaw)
	mux.HandleFunc("POST /api/preset/{name}", srv.HandleUpdatePresetModel)
	mux.HandleFunc("GET /api/preset/config", srv.HandleGetPresetConfig)
	mux.HandleFunc("PUT /api/preset/config", srv.HandlePutPresetConfig)
	mux.HandleFunc("GET /api/llamaswap/w84config", srv.HandleGetW84Config)
	mux.HandleFunc("PUT /api/llamaswap/w84config", srv.HandlePutW84Config)
	mux.HandleFunc("GET /api/llamaswap/raw/{name}", srv.HandleGetLlamaSwapRaw)
	mux.HandleFunc("PUT /api/llamaswap/raw/{name}", srv.HandlePutLlamaSwapRaw)
	// mux.HandleFunc("GET /api/llamaswap/groups/{name}", srv.handleGetLlamaSwapGroups)
	// mux.HandleFunc("PUT /api/llamaswap/groups/{name}", srv.handlePutLlamaSwapGroups)
	mux.HandleFunc("GET /api/llamaswap/config", srv.HandleGetLlamaSwapConfig)
	mux.HandleFunc("PUT /api/llamaswap/config", srv.HandlePutLlamaSwapConfig)
	mux.HandleFunc("POST /api/llamaswap/model", srv.HandleAddLlamaSwapModel)
	mux.HandleFunc("GET /api/llamaswap/models", srv.HandleLlamaSwapModels)
	mux.HandleFunc("GET /api/llamaswap/models/stream", srv.HandleLlamaSwapModelsStream)
	mux.HandleFunc("POST /api/llamaswap/models/load/{id...}", srv.HandleLlamaSwapLoadModel)
	mux.HandleFunc("POST /api/llamaswap/models/unload", srv.HandleLlamaSwapUnloadAll)
	mux.HandleFunc("POST /api/llamaswap/models/unload/{id...}", srv.HandleLlamaSwapUnloadModel)
	mux.HandleFunc("GET /api/llamaswap/settings", srv.HandleLlamaSwapSettings)
	mux.HandleFunc("GET /api/llamaswap/logs/stream", srv.HandleLlamaSwapLogStream)
	mux.HandleFunc("GET /api/llamaswap/logs/stream/{id...}", srv.HandleLlamaSwapLogStream)
	mux.HandleFunc("GET /api/editor/state", srv.HandleGetEditorState)
	mux.HandleFunc("PUT /api/editor/state", srv.HandlePutEditorState)
	mux.HandleFunc("POST /api/updates/apply", srv.HandleApplyUpdates)
	return mux
}
