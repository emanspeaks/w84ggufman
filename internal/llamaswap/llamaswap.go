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
func AddModel(doc *yaml.Node, name, modelPath, mmprojPath, vaePath string) {
	root := rootMapping(doc)

	models := mappingGet(root, "models")
	if models == nil {
		models = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mappingSet(root, "models", models)
	}

	sd := isSDModel(name, vaePath)
	var cmd string
	if sd {
		cmd = buildSDCmd(modelPath, vaePath)
	} else {
		cmd = buildLLMCmd(modelPath, name, mmprojPath)
	}

	ttl := 0
	if sd {
		ttl = 600
	} else {
		ttl = llmTTL(name)
	}

	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mappingSet(entry, "cmd", &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: cmd,
		Style: yaml.FoldedStyle,
	})
	mappingSet(entry, "ttl", &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!int",
		Value: strconv.Itoa(ttl),
	})
	if sd {
		mappingSet(entry, "checkEndpoint", &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: "/health",
		})
	}
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

// buildLLMCmd builds the llama-server command string for a text or vision LLM.
// The returned string uses embedded newlines so yaml.v3 serializes it as a
// folded block scalar (>-), matching the llama-swap config convention.
func buildLLMCmd(modelPath, name, mmprojPath string) string {
	parts := []string{
		"/ai/llama-swap/bin/llama-server --port ${PORT}",
		"-m " + modelPath,
	}
	if mmprojPath != "" {
		parts = append(parts, "--mmproj "+mmprojPath)
	}
	parts = append(parts,
		"--alias "+name,
		"--no-webui -ngl 999 --no-mmap --flash-attn --mlock -c 65536 --jinja",
	)
	return strings.Join(parts, "\n")
}

// buildSDCmd builds the sd-server command string for a Stable Diffusion model.
func buildSDCmd(modelPath, vaePath string) string {
	parts := []string{
		"/ai/llama-swap/bin/sd-server",
		"--listen-ip 127.0.0.1 --listen-port ${PORT}",
		"--diffusion-model " + modelPath,
	}
	if vaePath != "" {
		parts = append(parts, "--vae "+vaePath)
	}
	parts = append(parts, "--threads 16 --fa")
	return strings.Join(parts, "\n")
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
	mappingSet(root, "healthCheckTimeout", intNode(300))
	mappingSet(root, "logLevel", strNode("info"))
	mappingSet(root, "startPort", intNode(5900))
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	doc.Content = []*yaml.Node{root}
	return doc
}
