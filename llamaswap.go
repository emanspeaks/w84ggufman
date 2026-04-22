package main

import (
	"os"
	"path/filepath"

	"github.com/emanspeaks/w84ggufman/internal/llamaswap"
	"gopkg.in/yaml.v3"
)

type llamaSwapManager struct {
	path      string // path to config.yaml
	modelsDir string // models root directory (.w84ggufman.yaml lives here)
}

func newLlamaSwapManager(cfg Config) *llamaSwapManager {
	if cfg.LlamaSwapConfig == "" {
		return nil
	}
	return &llamaSwapManager{path: cfg.LlamaSwapConfig, modelsDir: cfg.ModelsDir}
}

// w84Config holds the w84ggufman-specific configuration stored in
// .w84ggufman.yaml in the models root directory.
type w84Config struct {
	Templates map[string]string `yaml:"templates,omitempty"`
}

func (m *llamaSwapManager) w84ConfigPath() string {
	return filepath.Join(m.modelsDir, ".w84ggufman.yaml")
}

func (m *llamaSwapManager) loadW84Config() w84Config {
	data, err := os.ReadFile(m.w84ConfigPath())
	if err != nil {
		return w84Config{}
	}
	var cfg w84Config
	yaml.Unmarshal(data, &cfg)
	return cfg
}

// defaultW84ConfigYAML is shown when no .w84ggufman.yaml file exists yet.
const defaultW84ConfigYAML = `templates:
  llm: |
    cmd: |
      ${llama-command-template}
        -c ${default-context}
        -m {{MODEL_PATH}}
        {{MMPROJ_LINE}}
        --alias {{MODEL_NAME}}
    ttl: -1
    metadata:
      model_type: llm
      port: ${PORT}
  sd: |
    cmd: |
      ${sd-command-template}
        --diffusion-model {{MODEL_PATH}}
        {{VAE_LINE}}
    ttl: 600
    checkEndpoint: ${sd-check-endpoint}
    metadata:
      model_type: sd
      port: ${PORT}
`

func (m *llamaSwapManager) readW84ConfigRaw() (string, error) {
	data, err := os.ReadFile(m.w84ConfigPath())
	if os.IsNotExist(err) {
		return defaultW84ConfigYAML, nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *llamaSwapManager) writeW84Config(body string) error {
	return os.WriteFile(m.w84ConfigPath(), []byte(body), 0664)
}

// AddModel adds or replaces a model entry in config.yaml using the template
// from .w84ggufman.yaml for the appropriate model type.
func (m *llamaSwapManager) AddModel(name, modelPath, mmprojPath, vaePath, modelType string) error {
	cfg := m.loadW84Config()
	return llamaswap.AddOrReplaceModelInFile(m.path, name, modelPath, mmprojPath, vaePath, modelType, cfg.Templates)
}

// RemoveModel removes a model entry from config.yaml.
func (m *llamaSwapManager) RemoveModel(name string) error {
	return llamaswap.RemoveModelFromFile(m.path, name)
}

// HasModel reports whether the named model is registered in config.yaml.
func (m *llamaSwapManager) HasModel(name string) (bool, error) {
	return llamaswap.HasModelInFile(m.path, name)
}

// ReadRaw returns the body of the named model entry for display in a text editor.
func (m *llamaSwapManager) ReadRaw(name string) (string, error) {
	return llamaswap.ReadModelRawFromFile(m.path, name)
}

// WriteRaw replaces the body of the named model entry.
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
