package main

import (
	"encoding/json"
	"os"
)

type Config struct {
	ModelsDir               string            `json:"modelsDir"`
	LlamaServerURL          string            `json:"llamaServerURL"`
	LlamaService            string            `json:"llamaService"`
	Port                    int               `json:"port"`
	HFToken                 string            `json:"hfToken"`
	WarnDownloadGiB         float64           `json:"warnDownloadGiB"`
	VramGiB                 float64           `json:"vramGiB"`         // 0 = auto-detect
	WarnVramPercent         float64           `json:"warnVramPercent"` // % of VRAM; default 80
	SelfService             string            `json:"selfService"`     // systemd unit for self-restart (empty = disabled)
	PresetGlobal            map[string]string `json:"presetGlobal"`
	LlamaSwapConfig         string            `json:"llamaSwapConfig"`         // path to llama-swap config.yaml; empty = disabled
	ForceRestartOnLlamaSwap bool              `json:"forceRestartOnLlamaSwap"` // restart service even when llama-swap hot-reload is active
	ShowDotFiles            bool              `json:"showDotFiles"`            // show dot/hidden dirs as model cards (default false)
	RootIgnorePatterns      []string          `json:"-"`                       // effective top-level ignore patterns resolved at startup
}

func defaultConfig() Config {
	return Config{
		ModelsDir:       "/var/lib/llama-models",
		LlamaServerURL:  "http://localhost:9292",
		LlamaService:    "llama-cpp.service",
		Port:            9293,
		HFToken:         "",
		WarnDownloadGiB: 10.0,
		VramGiB:         0,
		WarnVramPercent: 80,
		SelfService:     "w84ggufman.service",
		PresetGlobal: map[string]string{
			"ctx-size":     "65536",
			"flash-attn":   "on",
			"jinja":        "true",
			"n-gpu-layers": "999",
		},
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	if path == "" {
		cfg.resolveRootOverrides()
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.resolveRootOverrides()
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(stripJSONCComments(data), &cfg); err != nil {
		return cfg, err
	}
	cfg.resolveRootOverrides()
	return cfg, nil
}

// resolveRootOverrides merges startup overrides from modelsDir/.w84ggufman.json.
// Root-level "ignore" replaces built-in defaults when provided.
func (c *Config) resolveRootOverrides() {
	rootMeta := readModelMeta(c.ModelsDir)
	if len(rootMeta.Ignore) > 0 {
		c.RootIgnorePatterns = append([]string(nil), rootMeta.Ignore...)
		return
	}
	c.RootIgnorePatterns = append([]string(nil), defaultIgnorePatterns...)
}

// stripJSONCComments removes // and /* */ comments without mangling string literals.
func stripJSONCComments(src []byte) []byte {
	out := make([]byte, 0, len(src))
	i := 0
	inString := false
	for i < len(src) {
		if inString {
			if src[i] == '\\' && i+1 < len(src) {
				out = append(out, src[i], src[i+1])
				i += 2
				continue
			}
			if src[i] == '"' {
				inString = false
			}
			out = append(out, src[i])
			i++
			continue
		}
		if src[i] == '"' {
			inString = true
			out = append(out, src[i])
			i++
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			i += 2
			for i+1 < len(src) {
				if src[i] == '*' && src[i+1] == '/' {
					i += 2
					break
				}
				i++
			}
			continue
		}
		out = append(out, src[i])
		i++
	}
	return out
}
