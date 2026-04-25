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
	Groups          []string // group names this model is a member of
}

// ListModels returns every model entry in the document with its cmd-extracted
// paths and group memberships. Entries whose cmd cannot be parsed are silently
// skipped.
func ListModels(doc *yaml.Node) []ModelEntry {
	root := rootMapping(doc)
	models := mappingGet(root, "models")
	if models == nil {
		return nil
	}
	groupMap := buildGroupMap(root)
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
			ReferencedPaths: extractCmdPaths(cmd),
			IsSD:   isSD,
			Groups: groupMap[name],
		})
	}
	return result
}

// buildGroupMap returns a map of model name → group names parsed from the
// groups: section of the config document's root mapping node.
func buildGroupMap(root *yaml.Node) map[string][]string {
	groups := mappingGet(root, "groups")
	if groups == nil || groups.Kind != yaml.MappingNode {
		return nil
	}
	result := make(map[string][]string)
	for i := 0; i+1 < len(groups.Content); i += 2 {
		groupName := groups.Content[i].Value
		groupEntry := groups.Content[i+1]
		if groupEntry.Kind != yaml.MappingNode {
			continue
		}
		membersNode := mappingGet(groupEntry, "members")
		if membersNode == nil || membersNode.Kind != yaml.SequenceNode {
			continue
		}
		for _, m := range membersNode.Content {
			if m.Value != "" {
				result[m.Value] = append(result[m.Value], groupName)
			}
		}
	}
	return result
}
