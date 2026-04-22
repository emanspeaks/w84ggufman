package main

import (
	"io"
	"net/http"

	internalapi "github.com/emanspeaks/w84ggufman/internal/api"
)

type downloaderAdapter struct {
	d *downloader
}

func (a downloaderAdapter) ActiveInfo() (string, bool) {
	return a.d.activeInfo()
}

func (a downloaderAdapter) CancelDownload() {
	a.d.cancelDownload()
}

func (a downloaderAdapter) Start(repoID string, filenames []string, sidecarFiles []string, totalBytes int64, force bool) (bool, error) {
	return a.d.start(repoID, filenames, sidecarFiles, totalBytes, force)
}

func (a downloaderAdapter) QueueEntries() []internalapi.QueueEntry {
	entries := a.d.queueEntries()
	out := make([]internalapi.QueueEntry, len(entries))
	for i, e := range entries {
		out[i] = internalapi.QueueEntry{
			ID:         e.id,
			Label:      e.label,
			TotalBytes: e.totalBytes,
			RepoID:     e.repoID,
			Filenames:  append([]string(nil), e.filenames...),
		}
	}
	return out
}

func (a downloaderAdapter) RemoveFromQueue(id int64) bool {
	return a.d.removeFromQueue(id)
}

func (a downloaderAdapter) StreamSSE(w http.ResponseWriter, r *http.Request) {
	a.d.streamSSE(w, r)
}

type presetAdapter struct {
	p *presetManager
}

func (a presetAdapter) LoadView() (internalapi.PresetView, error) {
	f, err := a.p.Load()
	if err != nil {
		return internalapi.PresetView{}, err
	}
	return internalapi.PresetView{Global: f.Global, Sections: f.Sections}, nil
}

func (a presetAdapter) RemoveModel(name string) error {
	return a.p.RemoveModel(name)
}

func (a presetAdapter) UpdateGlobal(kvs map[string]string) error {
	return a.p.UpdateGlobal(kvs)
}

func (a presetAdapter) UpsertModelKeys(name string, kvs map[string]string) error {
	return a.p.UpsertModelKeys(name, kvs)
}

func (a presetAdapter) ReadRaw(name string) (string, error) {
	return a.p.ReadRaw(name)
}

func (a presetAdapter) WriteRaw(name, body string) error {
	return a.p.WriteRaw(name, body)
}

func (a presetAdapter) ReadAll() (string, error) {
	return a.p.ReadAll()
}

func (a presetAdapter) WriteAll(body string) error {
	return a.p.WriteAll(body)
}

type llamaSwapAdapter struct {
	l *llamaSwapManager
}

func (a llamaSwapAdapter) ListModels() ([]internalapi.LlamaSwapModelEntry, error) {
	models, err := a.l.ListModels()
	if err != nil {
		return nil, err
	}
	out := make([]internalapi.LlamaSwapModelEntry, 0, len(models))
	for _, m := range models {
		out = append(out, internalapi.LlamaSwapModelEntry{
			Name:            m.Name,
			ModelPath:       m.ModelPath,
			ReferencedPaths: append([]string(nil), m.ReferencedPaths...),
		})
	}
	return out, nil
}

func (a llamaSwapAdapter) RemoveModel(name string) error {
	return a.l.RemoveModel(name)
}

func (a llamaSwapAdapter) AddModel(name, modelPath, mmprojPath, vaePath, modelType string) error {
	return a.l.AddModel(name, modelPath, mmprojPath, vaePath, modelType)
}

func (a llamaSwapAdapter) HasModel(name string) (bool, error) {
	return a.l.HasModel(name)
}

func (a llamaSwapAdapter) LoadTemplates() any {
	return a.l.LoadTemplates()
}

func (a llamaSwapAdapter) ReadRaw(name string) (string, error) {
	return a.l.ReadRaw(name)
}

func (a llamaSwapAdapter) WriteRaw(name, body string) error {
	return a.l.WriteRaw(name, body)
}

func (a llamaSwapAdapter) UpdateTemplatesFromJSON(r io.Reader) error {
	return a.l.UpdateTemplatesFromJSON(r)
}

func (a llamaSwapAdapter) ReadAll() (string, error) {
	return a.l.ReadAll()
}

func (a llamaSwapAdapter) WriteAll(body string) error {
	return a.l.WriteAll(body)
}

func toAPIConfig(cfg Config) internalapi.Config {
	return internalapi.Config{
		ModelsDir:               cfg.ModelsDir,
		LlamaServerURL:          cfg.LlamaServerURL,
		LlamaService:            cfg.LlamaService,
		Port:                    cfg.Port,
		HFToken:                 cfg.HFToken,
		WarnDownloadGiB:         cfg.WarnDownloadGiB,
		VramGiB:                 cfg.VramGiB,
		WarnVramPercent:         cfg.WarnVramPercent,
		SelfService:             cfg.SelfService,
		AtopwebURL:              cfg.AtopwebURL,
		ForceRestartOnLlamaSwap: cfg.ForceRestartOnLlamaSwap,
		ShowDotFiles:            cfg.ShowDotFiles,
		RootIgnorePatterns:      append([]string(nil), cfg.RootIgnorePatterns...),
	}
}

func toAPIModelMeta(meta modelMeta) internalapi.ModelMeta {
	return internalapi.ModelMeta{
		RepoID:     meta.RepoID,
		SkipHFSync: meta.SkipHFSync,
		Ignore:     append([]string(nil), meta.Ignore...),
		CtxSize:    meta.CtxSize,
	}
}

func fromAPIModelMeta(meta internalapi.ModelMeta) modelMeta {
	return modelMeta{
		RepoID:     meta.RepoID,
		SkipHFSync: meta.SkipHFSync,
		Ignore:     append([]string(nil), meta.Ignore...),
		CtxSize:    meta.CtxSize,
	}
}

func toAPIRepoInfo(info *HFRepoInfo) *internalapi.RepoInfo {
	out := &internalapi.RepoInfo{
		IsVision:     info.IsVision,
		PipelineTag:  info.PipelineTag,
		Tags:         append([]string(nil), info.Tags...),
		PresentFiles: append([]string(nil), info.PresentFiles...),
		RogueFiles:   append([]string(nil), info.RogueFiles...),
		LocalOnly:    info.LocalOnly,
		Models:       make([]internalapi.RepoFile, 0, len(info.Models)),
		Sidecars:     make([]internalapi.RepoFile, 0, len(info.Sidecars)),
	}
	for _, f := range info.Models {
		out.Models = append(out.Models, internalapi.RepoFile{
			Filename:    f.Filename,
			Size:        f.Size,
			DownloadURL: f.DownloadURL,
			DisplayName: f.DisplayName,
		})
	}
	for _, f := range info.Sidecars {
		out.Sidecars = append(out.Sidecars, internalapi.RepoFile{
			Filename:    f.Filename,
			Size:        f.Size,
			DownloadURL: f.DownloadURL,
			DisplayName: f.DisplayName,
		})
	}
	return out
}
