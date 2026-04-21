package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/emanspeaks/w84ggufman/internal/llamaswap"
)

// llamaSwapManager manages the llama-swap config.yaml file in parallel with
// models.ini. The models.ini code is preserved so the setup can be reverted;
// this manager is only active when LlamaSwapConfig is set in the config.
//
// All mutations are line-based edits that touch only the target block,
// preserving comments, scalar styles, key ordering, and surrounding
// whitespace. See internal/llamaswap/config_ops.go.
type llamaSwapManager struct {
	path string
}

// newLlamaSwapManager returns a manager for the given config, or nil if
// LlamaSwapConfig is empty (feature disabled).
func newLlamaSwapManager(cfg Config) *llamaSwapManager {
	if cfg.LlamaSwapConfig == "" {
		return nil
	}
	return &llamaSwapManager{path: cfg.LlamaSwapConfig}
}

// AddModel adds or replaces a model entry in config.yaml and registers it in
// the appropriate group. modelType ("llm" or "sd") selects the template; if
// empty, it is inferred from name/vaePath. vaePath is the path to the VAE
// (ae.safetensors) for Stable Diffusion models. mmprojPath is the vision
// projector for multimodal LLMs.
func (m *llamaSwapManager) AddModel(name, modelPath, mmprojPath, vaePath, modelType string) error {
	return llamaswap.AddOrReplaceModelInFile(m.path, name, modelPath, mmprojPath, vaePath, modelType, m.LoadTemplates())
}

// templatesPath returns the path to the JSON templates file stored alongside
// config.yaml.
func (m *llamaSwapManager) templatesPath() string {
	return filepath.Join(filepath.Dir(m.path), "w84ggufman-templates.json")
}

// LoadTemplates reads the templates file, returning defaults if not found or
// unreadable.
func (m *llamaSwapManager) LoadTemplates() llamaswap.Templates {
	data, err := os.ReadFile(m.templatesPath())
	if err != nil {
		return llamaswap.DefaultTemplates()
	}
	var tpl llamaswap.Templates
	if err := json.Unmarshal(data, &tpl); err != nil {
		return llamaswap.DefaultTemplates()
	}
	return tpl
}

// SaveTemplates writes the templates file.
func (m *llamaSwapManager) SaveTemplates(tpl llamaswap.Templates) error {
	data, err := json.MarshalIndent(tpl, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.templatesPath(), data, 0664)
}

// UpdateTemplatesFromJSON parses JSON from r and saves the templates file.
func (m *llamaSwapManager) UpdateTemplatesFromJSON(r io.Reader) error {
	var tpl llamaswap.Templates
	if err := json.NewDecoder(r).Decode(&tpl); err != nil {
		return fmt.Errorf("invalid templates JSON: %w", err)
	}
	return m.SaveTemplates(tpl)
}

// RemoveModel removes a model entry from config.yaml and from all group
// member lists.
func (m *llamaSwapManager) RemoveModel(name string) error {
	return llamaswap.RemoveModelFromFile(m.path, name)
}

// HasModel reports whether the named model is registered in config.yaml.
func (m *llamaSwapManager) HasModel(name string) (bool, error) {
	return llamaswap.HasModelInFile(m.path, name)
}

// ReadRaw returns the body of the named model entry as it appears in the
// file, suitable for display in a text editor.
func (m *llamaSwapManager) ReadRaw(name string) (string, error) {
	return llamaswap.ReadModelRawFromFile(m.path, name)
}

// WriteRaw replaces the body of the named model entry with the supplied
// text, leaving the rest of the file untouched.
func (m *llamaSwapManager) WriteRaw(name, body string) error {
	return llamaswap.WriteModelRawToFile(m.path, name, body)
}

// ListModels returns all model entries in config.yaml with their cmd-extracted paths.
func (m *llamaSwapManager) ListModels() ([]llamaswap.ModelEntry, error) {
	return llamaswap.ListModelsFromFile(m.path)
}

// ReadAll returns the full contents of config.yaml as a string.
func (m *llamaSwapManager) ReadAll() (string, error) {
	data, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

// WriteAll writes body as the full contents of config.yaml.
func (m *llamaSwapManager) WriteAll(body string) error {
	return os.WriteFile(m.path, []byte(body), 0664)
}
