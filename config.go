package main

import (
	"encoding/json"
	"os"
)

type Config struct {
	ModelsDir        string  `json:"modelsDir"`
	LlamaServerURL   string  `json:"llamaServerURL"`
	LlamaService     string  `json:"llamaService"`
	Port             int     `json:"port"`
	HFToken          string  `json:"hfToken"`
	WarnDownloadGiB  float64 `json:"warnDownloadGiB"`
}

func defaultConfig() Config {
	return Config{
		ModelsDir:       "/var/lib/llama-models",
		LlamaServerURL:  "http://localhost:9292",
		LlamaService:    "llama-cpp.service",
		Port:            9293,
		HFToken:         "",
		WarnDownloadGiB: 10.0,
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(stripJSONCComments(data), &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
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
