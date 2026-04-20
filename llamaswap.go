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
// the appropriate group. vaePath is the path to the VAE (ae.safetensors) for
// Stable Diffusion models; it also serves as the signal that the model is SD.
// mmprojPath is the vision projector for multimodal LLMs.
func (m *llamaSwapManager) AddModel(name, modelPath, mmprojPath, vaePath string) error {
	tpl := m.LoadTemplates()
	doc, err := llamaswap.LoadFile(m.path)
	if err != nil {
		return err
	}
	llamaswap.AddModel(doc, name, modelPath, mmprojPath, vaePath, tpl)
	return llamaswap.WriteFile(m.path, doc)
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
	doc, err := llamaswap.LoadFile(m.path)
	if err != nil {
		return err
	}
	llamaswap.RemoveModel(doc, name)
	return llamaswap.WriteFile(m.path, doc)
}

// HasModel reports whether the named model is registered in config.yaml.
func (m *llamaSwapManager) HasModel(name string) (bool, error) {
	doc, err := llamaswap.LoadFile(m.path)
	if err != nil {
		return false, err
	}
	return llamaswap.HasModel(doc, name), nil
}

// ReadRaw returns the YAML block for a single model entry, suitable for
// display in a text editor.
func (m *llamaSwapManager) ReadRaw(name string) (string, error) {
	doc, err := llamaswap.LoadFile(m.path)
	if err != nil {
		return "", err
	}
	return llamaswap.ReadModelRaw(doc, name)
}

// WriteRaw parses body as a YAML mapping and replaces the named model's entry
// in config.yaml.
func (m *llamaSwapManager) WriteRaw(name, body string) error {
	doc, err := llamaswap.LoadFile(m.path)
	if err != nil {
		return err
	}
	if err := llamaswap.WriteModelRaw(doc, name, body); err != nil {
		return err
	}
	return llamaswap.WriteFile(m.path, doc)
}

// ListModels returns all model entries in config.yaml with their cmd-extracted paths.
func (m *llamaSwapManager) ListModels() ([]llamaswap.ModelEntry, error) {
	doc, err := llamaswap.LoadFile(m.path)
	if err != nil {
		return nil, err
	}
	return llamaswap.ListModels(doc), nil
}

// // ListGroups returns all groups in config.yaml annotated with membership for modelName.
// func (m *llamaSwapManager) ListGroups(modelName string) ([]llamaswap.GroupInfo, error) {
// 	doc, err := llamaswap.LoadFile(m.path)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return llamaswap.ListGroups(doc, modelName), nil
// }

// // SetGroupMembership updates group membership for modelName and saves the file.
// func (m *llamaSwapManager) SetGroupMembership(modelName string, groupNames []string) error {
// 	doc, err := llamaswap.LoadFile(m.path)
// 	if err != nil {
// 		return err
// 	}
// 	llamaswap.SetGroupMembership(doc, modelName, groupNames)
// 	return llamaswap.WriteFile(m.path, doc)
// }

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
