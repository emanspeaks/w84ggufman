package main

import (
	"os"
	"path/filepath"

	"github.com/emanspeaks/w84ggufman/internal/ini"
)

type presetManager struct {
	path string
	cfg  Config
}

func newPresetManager(cfg Config) *presetManager {
	return &presetManager{
		path: filepath.Join(cfg.ModelsDir, "models.ini"),
		cfg:  cfg,
	}
}

func (p *presetManager) Load() (*ini.File, error) {
	f, err := ini.ParseFile(p.path)
	if err != nil {
		return nil, err
	}
	// Seed global defaults for any keys not already present (display only —
	// this does not write to disk).
	for k, v := range p.cfg.PresetGlobal {
		if _, exists := f.Global[k]; !exists {
			f.Global[k] = v
		}
	}
	if len(f.Header) == 0 {
		f.Header = []string{"; managed by w84ggufman — manual edits are preserved"}
	}
	return f, nil
}

func (p *presetManager) Save(f *ini.File) error {
	return f.WriteFile(p.path)
}

func (p *presetManager) AddModel(name, modelPath, mmprojPath string) error {
	kvs := map[string]string{"model": modelPath}
	if mmprojPath != "" {
		kvs["mmproj"] = mmprojPath
	}
	return ini.AppendSection(p.path, name, kvs)
}

func (p *presetManager) RemoveModel(name string) error {
	return ini.RemoveSection(p.path, name)
}

func (p *presetManager) UpdateGlobal(kvs map[string]string) error {
	return ini.UpsertSectionKeys(p.path, "*", kvs)
}

func (p *presetManager) UpsertModelKeys(name string, kvs map[string]string) error {
	return ini.UpsertSectionKeys(p.path, name, kvs)
}

// ReadRaw returns the raw body text of the model's section in models.ini,
// preserving comments and blank lines exactly as they appear.
func (p *presetManager) ReadRaw(name string) (string, error) {
	return ini.ReadSectionRaw(p.path, name)
}

// WriteRaw replaces the body of the model's section with raw text,
// keeping the section's position in the file.
func (p *presetManager) WriteRaw(name, body string) error {
	return ini.ReplaceSectionBody(p.path, name, body)
}

func (p *presetManager) HasModel(name string) (bool, error) {
	f, err := p.Load()
	if err != nil {
		return false, err
	}
	_, ok := f.Sections[name]
	return ok, nil
}

// ReadAll returns the full contents of models.ini as a string.
func (p *presetManager) ReadAll() (string, error) {
	data, err := os.ReadFile(p.path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

// WriteAll writes body as the full contents of models.ini.
func (p *presetManager) WriteAll(body string) error {
	return os.WriteFile(p.path, []byte(body), 0664)
}
