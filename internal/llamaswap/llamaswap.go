package llamaswap

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadFile parses the llama-swap config.yaml at path into a YAML document node.
// If the file does not exist, returns a minimal document with default global settings.
func LoadFile(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return newEmptyDoc(), nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return newEmptyDoc(), nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing config.yaml: %w", err)
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return &doc, nil
	}
	return newEmptyDoc(), nil
}

// WriteFile serializes the document node to path, using 2-space indentation
// to match llama-swap conventions.
func WriteFile(path string, doc *yaml.Node) error {
	root := rootMapping(doc)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	err = enc.Encode(root)
	if closeErr := enc.Close(); err == nil {
		err = closeErr
	}
	return err
}

// AddModel adds or replaces a model entry in the config document and registers
// it in the appropriate group (llm or sd). vaePath is the path to ae.safetensors
// for Stable Diffusion models; it also serves as the signal that the model is an
// SD model. mmprojPath is the vision projector for multimodal LLMs.
// tpl supplies the cmd template and TTL defaults.
func AddModel(doc *yaml.Node, name, modelPath, mmprojPath, vaePath string, tpl Templates) {
	root := rootMapping(doc)

	models := mappingGet(root, "models")
	if models == nil {
		models = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mappingSet(root, "models", models)
	}

	sd := isSDModel(name, vaePath)
	var cmd string
	var ttl int
	if sd {
		cmd = ApplySDCmd(tpl, modelPath, vaePath)
		ttl = tpl.SDTtl
	} else {
		cmd = ApplyLLMCmd(tpl, modelPath, name, mmprojPath)
		ttl = LLMTtlFor(tpl, name)
	}

	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mappingSet(entry, "cmd", &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: cmd,
		Style: yaml.LiteralStyle,
	})
	mappingSet(entry, "ttl", &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!int",
		Value: strconv.Itoa(ttl),
	})
	if sd {
		checkEndpoint := tpl.SDCheckEndpoint
		if checkEndpoint == "" {
			checkEndpoint = DefaultSDCheckEndpoint
		}
		mappingSet(entry, "checkEndpoint", &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: checkEndpoint,
		})
	}
	modelType := "llm"
	if sd {
		modelType = "sd"
	}
	meta := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mappingSet(meta, "model_type", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: modelType})
	mappingSet(meta, "port", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "${PORT}"})
	mappingSet(entry, "metadata", meta)
	mappingSet(models, name, entry)

	groups := mappingGet(root, "groups")
	if groups == nil {
		groups = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mappingSet(root, "groups", groups)
	}

	groupName := "llm"
	if sd {
		groupName = "sd"
	}

	group := mappingGet(groups, groupName)
	if group == nil {
		group = buildDefaultGroup(sd)
		mappingSet(groups, groupName, group)
	}

	members := mappingGet(group, "members")
	if members == nil {
		members = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		mappingSet(group, "members", members)
	}
	seqAppend(members, name)
}

// RemoveModel removes the named model from the models section and from all
// group member lists in the config document.
func RemoveModel(doc *yaml.Node, name string) {
	root := rootMapping(doc)

	models := mappingGet(root, "models")
	if models != nil {
		mappingDelete(models, name)
	}

	groups := mappingGet(root, "groups")
	if groups == nil {
		return
	}
	for i := 1; i < len(groups.Content); i += 2 {
		group := groups.Content[i]
		if group.Kind != yaml.MappingNode {
			continue
		}
		members := mappingGet(group, "members")
		if members != nil {
			seqRemove(members, name)
		}
	}
}

// HasModel reports whether name appears as a key in the models section.
func HasModel(doc *yaml.Node, name string) bool {
	root := rootMapping(doc)
	models := mappingGet(root, "models")
	return models != nil && mappingGet(models, name) != nil
}

// ReadModelRaw serializes the named model's entry (the YAML mapping under its
// key) to a plain-text YAML block suitable for display in a text editor.
// Returns an empty string if the model is not present.
func ReadModelRaw(doc *yaml.Node, name string) (string, error) {
	root := rootMapping(doc)
	models := mappingGet(root, "models")
	if models == nil {
		return "", nil
	}
	entry := mappingGet(models, name)
	if entry == nil {
		return "", nil
	}
	var sb strings.Builder
	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	if err := enc.Encode(entry); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// WriteModelRaw parses body as a YAML mapping and replaces the named model's
// entry in the models section. Returns an error if the model is not present or
// body is not valid YAML.
func WriteModelRaw(doc *yaml.Node, name, body string) error {
	root := rootMapping(doc)
	models := mappingGet(root, "models")
	if models == nil {
		return fmt.Errorf("no models section in config.yaml")
	}
	if mappingGet(models, name) == nil {
		return fmt.Errorf("model %q not found in config.yaml", name)
	}
	var parsed yaml.Node
	if err := yaml.Unmarshal([]byte(body), &parsed); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	entry := rootMapping(&parsed)
	if entry == nil || entry.Kind != yaml.MappingNode {
		return fmt.Errorf("YAML body must be a mapping (key: value pairs)")
	}
	mappingSet(models, name, entry)
	return nil
}

// ModelEntry describes a model registered in the config.yaml models section,
// with paths extracted from the cmd field.
type ModelEntry struct {
	Name       string
	ModelPath  string // from -m flag (LLM) or --diffusion-model flag (SD)
	MmprojPath string // from --mmproj flag, empty if none
	IsSD       bool   // true for sd-server entries
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
			IsSD:       isSD,
		})
	}
	return result
}

// extractCmdFlag finds the value after flag in a cmd string. Handles
// newline-separated, backslash-continuation, and single-line formats.
func extractCmdFlag(cmd, flag string) string {
	// Strip backslash-newline continuations so tokens are adjacent.
	cmd = strings.ReplaceAll(cmd, "\\\n", " ")
	cmd = strings.ReplaceAll(cmd, "\\\r\n", " ")
	tokens := strings.Fields(cmd)
	for i, t := range tokens {
		if t == flag && i+1 < len(tokens) {
			return tokens[i+1]
		}
	}
	return ""
}

// GroupInfo describes one group entry in the llama-swap config.
type GroupInfo struct {
	Name      string `json:"name"`
	Swap      bool   `json:"swap"`
	Exclusive bool   `json:"exclusive"`
	IsMember  bool   `json:"isMember"`
}

// ListGroups returns every group defined in the document, annotated with
// whether modelName is currently a member.
func ListGroups(doc *yaml.Node, modelName string) []GroupInfo {
	root := rootMapping(doc)
	groups := mappingGet(root, "groups")
	if groups == nil {
		return nil
	}
	var result []GroupInfo
	for i := 0; i+1 < len(groups.Content); i += 2 {
		name := groups.Content[i].Value
		group := groups.Content[i+1]
		if group.Kind != yaml.MappingNode {
			continue
		}
		info := GroupInfo{Name: name}
		if sv := mappingGet(group, "swap"); sv != nil {
			info.Swap, _ = strconv.ParseBool(sv.Value)
		}
		if ev := mappingGet(group, "exclusive"); ev != nil {
			info.Exclusive, _ = strconv.ParseBool(ev.Value)
		}
		if members := mappingGet(group, "members"); members != nil {
			info.IsMember = seqContains(members, modelName)
		}
		result = append(result, info)
	}
	return result
}

// SetGroupMembership adds modelName to each group in groupNames and removes it
// from all other groups. Groups not already present in the document are ignored.
func SetGroupMembership(doc *yaml.Node, modelName string, groupNames []string) {
	root := rootMapping(doc)
	groups := mappingGet(root, "groups")
	if groups == nil {
		return
	}
	wanted := make(map[string]bool, len(groupNames))
	for _, g := range groupNames {
		wanted[g] = true
	}
	for i := 0; i+1 < len(groups.Content); i += 2 {
		name := groups.Content[i].Value
		group := groups.Content[i+1]
		if group.Kind != yaml.MappingNode {
			continue
		}
		members := mappingGet(group, "members")
		if members == nil {
			if !wanted[name] {
				continue
			}
			members = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			mappingSet(group, "members", members)
		}
		if wanted[name] {
			seqAppend(members, modelName)
		} else {
			seqRemove(members, modelName)
		}
	}
}

// --- yaml.Node helpers ---

func rootMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return doc
}

func mappingGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mappingSet(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		value,
	)
}

func mappingDelete(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func seqContains(seq *yaml.Node, value string) bool {
	for _, n := range seq.Content {
		if n.Value == value {
			return true
		}
	}
	return false
}

func seqAppend(seq *yaml.Node, value string) {
	if !seqContains(seq, value) {
		seq.Content = append(seq.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: value})
	}
}

func seqRemove(seq *yaml.Node, value string) {
	for i, n := range seq.Content {
		if n.Value == value {
			seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
			return
		}
	}
}

// --- model classification and command builders ---

// paramCountRe matches a parameter count like "3B", "70B", "0.5B" in a model name.
var paramCountRe = regexp.MustCompile(`(?i)[-_](\d+(?:\.\d+)?)B(?:[-_.]|$)`)

func parseBillionParams(name string) float64 {
	if m := paramCountRe.FindStringSubmatch(name); m != nil {
		v, _ := strconv.ParseFloat(m[1], 64)
		return v
	}
	return 0
}

// isSDModel returns true for Stable Diffusion / Flux image generation models.
// A non-empty vaePath is definitive; otherwise the name prefix is used as a
// heuristic (llama-swap convention: SD models are named "flux-*" or "sd-*").
func isSDModel(name, vaePath string) bool {
	if vaePath != "" {
		return true
	}
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "flux-") || strings.HasPrefix(lower, "sd-")
}

// llmTTL returns the recommended TTL for an LLM based on its parameter count.
// Models under 10B get 600 s; larger or unknown models get 0 (never evict).
func llmTTL(name string) int {
	b := parseBillionParams(name)
	if b > 0 && b < 10 {
		return 600
	}
	return 0
}

// buildDefaultGroup returns a new group mapping node with the correct swap /
// exclusive defaults for the group type.
func buildDefaultGroup(sd bool) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	swapVal := "false"
	if sd {
		swapVal = "true"
	}
	mappingSet(node, "swap", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: swapVal})
	mappingSet(node, "exclusive", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"})
	mappingSet(node, "members", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})
	return node
}

// newEmptyDoc returns a document node with the default llama-swap global settings.
func newEmptyDoc() *yaml.Node {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	intNode := func(v int) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}
	}
	strNode := func(v string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
	}
	boolNode := func(v bool) *yaml.Node {
		val := "false"
		if v {
			val = "true"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: val}
	}
	mappingSet(root, "healthCheckTimeout", intNode(300))
	mappingSet(root, "logLevel", strNode("info"))
	mappingSet(root, "startPort", intNode(5901))
	mappingSet(root, "logToStdout", strNode("both"))
	mappingSet(root, "globalTTL", intNode(600))
	mappingSet(root, "sendLoadingState", boolNode(true))
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	doc.Content = []*yaml.Node{root}
	return doc
}
