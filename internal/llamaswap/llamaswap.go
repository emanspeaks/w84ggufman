package llamaswap

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadFile parses the llama-swap config.yaml at path into a YAML document
// node for the read-only list path (ListModels). Write operations use the
// line-based editors in config_ops.go so formatting and comments survive
// round-trips. If the file does not exist, an empty document is returned.
func LoadFile(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &yaml.Node{Kind: yaml.DocumentNode}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return &yaml.Node{Kind: yaml.DocumentNode}, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing config.yaml: %w", err)
	}
	return &doc, nil
}
