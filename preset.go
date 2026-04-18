package main

import (
	"fmt"
	"path/filepath"

	"github.com/emanspeaks/w84ggufman/internal/ini"
)

type presetManager struct {
	path string
	cfg  Config
}

func newPresetManager(cfg Config) *presetManager {
	return &presetManager{
		path: filepath.Join(cfg.ModelsDir, "managed.ini"),
		cfg:  cfg,
	}
}

func (p *presetManager) Load() (*ini.File, error) {
	f, err := ini.ParseFile(p.path)
	if err != nil {
		return nil, err
	}
	// Seed global defaults for any keys not already present.
	for k, v := range p.cfg.PresetGlobal {
		if _, exists := f.Global[k]; !exists {
			f.Global[k] = v
		}
	}
	if len(f.Header) == 0 {
		f.Header = []string{
			"; managed by w84ggufman",
			"; do not edit manually",
		}
	}
	return f, nil
}

func (p *presetManager) Save(f *ini.File) error {
	return f.WriteFile(p.path)
}

func (p *presetManager) AddModel(name, modelPath, mmprojPath string) error {
	f, err := p.Load()
	if err != nil {
		return fmt.Errorf("preset load: %w", err)
	}
	sec := map[string]string{"model": modelPath}
	if mmprojPath != "" {
		sec["mmproj"] = mmprojPath
	}
	f.Sections[name] = sec
	return p.Save(f)
}

func (p *presetManager) RemoveModel(name string) error {
	f, err := p.Load()
	if err != nil {
		return fmt.Errorf("preset load: %w", err)
	}
	delete(f.Sections, name)
	return p.Save(f)
}

func (p *presetManager) UpdateGlobal(kvs map[string]string) error {
	f, err := p.Load()
	if err != nil {
		return fmt.Errorf("preset load: %w", err)
	}
	for k, v := range kvs {
		f.Global[k] = v
	}
	return p.Save(f)
}

func (p *presetManager) HasModel(name string) (bool, error) {
	f, err := p.Load()
	if err != nil {
		return false, err
	}
	_, ok := f.Sections[name]
	return ok, nil
}
