package llamaswap

import (
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func extractCmdPaths(cmd string, flags ...string) []string {
	cmd = strings.ReplaceAll(cmd, "\\\n", " ")
	cmd = strings.ReplaceAll(cmd, "\\\r\n", " ")
	tokens := strings.Fields(cmd)
	flagSet := make(map[string]struct{}, len(flags))
	for _, f := range flags {
		flagSet[f] = struct{}{}
	}
	out := make([]string, 0)
	seen := make(map[string]struct{})
	add := func(p string) {
		p = strings.TrimSpace(strings.Trim(p, `"'`))
		if p == "" || strings.HasPrefix(p, "-") {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for i, t := range tokens {
		if _, ok := flagSet[t]; ok {
			if i+1 < len(tokens) {
				add(tokens[i+1])
			}
			continue
		}
		for _, f := range flags {
			prefix := f + "="
			if strings.HasPrefix(t, prefix) {
				add(strings.TrimPrefix(t, prefix))
				break
			}
		}
	}
	return out
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
