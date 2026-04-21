package llamaswap

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// ModelEntry describes a model registered in the config.yaml models section,
// with paths extracted from the cmd field.
type ModelEntry struct {
	Name            string
	ModelPath       string   // from -m flag (LLM) or --diffusion-model flag (SD)
	MmprojPath      string   // from --mmproj flag, empty if none
	ReferencedPaths []string // all known path-bearing cmd args
	IsSD            bool     // true for sd-server entries
}

// ListModels returns every model entry in the document with its cmd-extracted
// paths. Entries whose cmd cannot be parsed are silently skipped.
func ListModels(doc *yaml.Node) []ModelEntry {
	root := rootMapping(doc)
	models := mappingGet(root, "models")
	if models == nil {
		return nil
	}
	var result []ModelEntry
	for i := 0; i+1 < len(models.Content); i += 2 {
		name := models.Content[i].Value
		entry := models.Content[i+1]
		if entry.Kind != yaml.MappingNode {
			continue
		}
		cmdNode := mappingGet(entry, "cmd")
		if cmdNode == nil {
			continue
		}
		cmd := cmdNode.Value
		modelPath := extractCmdFlag(cmd, "-m")
		isSD := false
		if modelPath == "" {
			modelPath = extractCmdFlag(cmd, "--diffusion-model")
			if modelPath != "" {
				isSD = true
			} else {
				isSD = strings.Contains(cmd, "sd-server")
			}
		}
		result = append(result, ModelEntry{
			Name:       name,
			ModelPath:  modelPath,
			MmprojPath: extractCmdFlag(cmd, "--mmproj"),
			ReferencedPaths: extractCmdPaths(cmd,
				"-m",
				"--model",
				"--diffusion-model",
				"--mmproj",
				"--vae",
				"--vae-path",
				"--clip_l",
				"--clip_g",
				"--clip-l",
				"--clip-g",
				"--t5xxl",
				"--control-net",
				"--controlnet",
				"--upscale-model",
				"--taesd",
			),
			IsSD: isSD,
		})
	}
	return result
}
