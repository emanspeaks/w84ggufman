package main

import (
	"os"

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
	doc, err := llamaswap.LoadFile(m.path)
	if err != nil {
		return err
	}
	llamaswap.AddModel(doc, name, modelPath, mmprojPath, vaePath)
	return llamaswap.WriteFile(m.path, doc)
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
