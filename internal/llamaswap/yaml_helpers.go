package llamaswap

import "gopkg.in/yaml.v3"

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
