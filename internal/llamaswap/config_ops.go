package llamaswap

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Line-based editors for llama-swap config.yaml. These operations touch only
// the affected model entry (or group-members line) and preserve all other
// bytes — comments, blank lines, scalar styles, key ordering — unchanged.
// The package also exposes yaml.Node-based readers (LoadFile, ListModels) for
// the read-only "show me the parsed model list" path.

// llama-swap convention: two-space indentation throughout.
const (
	indentModelName = 2 // `  <name>:` under models:
	indentModelBody = 4 // body of a model entry
)

func splitYamlLines(src string) []string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	lines := strings.Split(src, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func joinYamlLines(lines []string) string {
	return strings.Join(lines, "\n") + "\n"
}

func leadingSpaces(line string) int {
	n := 0
	for n < len(line) && line[n] == ' ' {
		n++
	}
	return n
}

// isStructural reports whether a line contributes to the YAML structure
// (non-blank and not a pure comment). Blank and comment-only lines are
// invisible to block-boundary detection so they ride with the block they
// belong to.
func isStructural(line string) bool {
	t := strings.TrimLeft(line, " \t")
	return t != "" && !strings.HasPrefix(t, "#")
}

// keyAtIndent reports whether line is a mapping entry "<key>:" at exactly
// targetIndent leading spaces.
func keyAtIndent(line, key string, targetIndent int) bool {
	if leadingSpaces(line) != targetIndent {
		return false
	}
	rest := line[targetIndent:]
	if !strings.HasPrefix(rest, key+":") {
		return false
	}
	tail := rest[len(key)+1:]
	if tail == "" {
		return true
	}
	c := tail[0]
	return c == ' ' || c == '\t' || c == '#'
}

// findKey scans lines[from:to) for the first structural line that is the
// mapping entry for key at exactly targetIndent. Returns -1 if absent.
func findKey(lines []string, from, to int, key string, targetIndent int) int {
	for i := from; i < to; i++ {
		if !isStructural(lines[i]) {
			continue
		}
		if keyAtIndent(lines[i], key, targetIndent) {
			return i
		}
	}
	return -1
}

// blockEnd returns the exclusive end of a block whose header is at
// lines[start] with indent headerIndent. The block ends at the first
// structural line at indent <= headerIndent within [start+1, upper), or
// upper if no such line exists.
func blockEnd(lines []string, start, headerIndent, upper int) int {
	for i := start + 1; i < upper; i++ {
		if !isStructural(lines[i]) {
			continue
		}
		if leadingSpaces(lines[i]) <= headerIndent {
			return i
		}
	}
	return upper
}

func indentBlock(body []string, n int) []string {
	out := make([]string, len(body))
	prefix := strings.Repeat(" ", n)
	for i, l := range body {
		if strings.TrimSpace(l) == "" {
			out[i] = ""
		} else {
			out[i] = prefix + l
		}
	}
	return out
}

func readConfigLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return splitYamlLines(string(data)), nil
}

func writeConfigLines(path string, lines []string) error {
	return os.WriteFile(path, []byte(joinYamlLines(lines)), 0664)
}

// findModelBlock locates a model named name within the models: section.
// Returns -1 for any of the indices that are not present.
func findModelBlock(lines []string, name string) (modelsStart, modelsEnd, nameStart, nameEnd int) {
	modelsStart = findKey(lines, 0, len(lines), "models", 0)
	if modelsStart == -1 {
		return -1, -1, -1, -1
	}
	modelsEnd = blockEnd(lines, modelsStart, 0, len(lines))
	nameStart = findKey(lines, modelsStart+1, modelsEnd, name, indentModelName)
	if nameStart == -1 {
		return modelsStart, modelsEnd, -1, -1
	}
	nameEnd = blockEnd(lines, nameStart, indentModelName, modelsEnd)
	return
}

// HasModelInFile reports whether the named model entry exists in the file
// at path. Returns (false, nil) if the file does not exist.
func HasModelInFile(path, name string) (bool, error) {
	lines, err := readConfigLines(path)
	if err != nil {
		return false, err
	}
	_, _, ns, _ := findModelBlock(lines, name)
	return ns != -1, nil
}

// ReadModelRawFromFile returns the body of the named model — every line
// indented under `  name:` — with the 4-space indent stripped for display.
// Trailing blank lines within the body are trimmed. Returns "" (no error)
// if the model or the file is absent.
func ReadModelRawFromFile(path, name string) (string, error) {
	lines, err := readConfigLines(path)
	if err != nil {
		return "", err
	}
	_, _, ns, ne := findModelBlock(lines, name)
	if ns == -1 {
		return "", nil
	}
	body := append([]string(nil), lines[ns+1:ne]...)
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	if len(body) == 0 {
		return "", nil
	}
	for i, l := range body {
		if leadingSpaces(l) >= indentModelBody {
			body[i] = l[indentModelBody:]
		}
	}
	return strings.Join(body, "\n"), nil
}

// WriteModelRawToFile replaces the body of the named model with newBody
// (at 0-indent; re-indented to 4 spaces). The `  name:` header line and
// all content outside the block are preserved exactly. Returns an error if
// the model is not present.
func WriteModelRawToFile(path, name, newBody string) error {
	lines, err := readConfigLines(path)
	if err != nil {
		return err
	}
	_, _, ns, ne := findModelBlock(lines, name)
	if ns == -1 {
		return fmt.Errorf("model %q not found in config.yaml", name)
	}
	bodyLines := indentBlock(splitYamlLines(newBody), indentModelBody)
	out := make([]string, 0, len(lines)-(ne-ns-1)+len(bodyLines))
	out = append(out, lines[:ns+1]...)
	out = append(out, bodyLines...)
	out = append(out, lines[ne:]...)
	return writeConfigLines(path, out)
}

// RemoveModelFromFile removes the named model from the models: section.
// Blank lines adjacent to the removed block collapse to at most one
// separator, and trailing comments attached to the next sibling survive.
// Groups are not touched — membership cleanup is the user's responsibility.
// No-ops if the model or the file is absent.
func RemoveModelFromFile(path, name string) error {
	lines, err := readConfigLines(path)
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return nil
	}
	_, _, ns, ne := findModelBlock(lines, name)
	if ns == -1 {
		return nil
	}
	dropStart := ns
	for dropStart > 0 && strings.TrimSpace(lines[dropStart-1]) == "" {
		dropStart--
	}
	if dropStart > 0 && ne < len(lines) {
		dropStart++
	}
	if dropStart > ns {
		dropStart = ns
	}
	// Peel trailing blank and model-indent-or-shallower comment lines back
	// to the following sibling so comments attached to the next entry
	// survive.
	dropEnd := ne
	for dropEnd > ns+1 {
		prev := lines[dropEnd-1]
		t := strings.TrimSpace(prev)
		if t == "" {
			dropEnd--
			continue
		}
		if strings.HasPrefix(t, "#") && leadingSpaces(prev) <= indentModelName {
			dropEnd--
			continue
		}
		break
	}
	out := append(append([]string(nil), lines[:dropStart]...), lines[dropEnd:]...)
	return writeConfigLines(path, out)
}

// AddOrReplaceModelInFile upserts the named model entry in the file at path
// using the supplied template. LLM entries are inserted at the top of the
// models: section; SD entries are appended at the bottom. Existing entries
// are replaced in place. Groups are never touched — membership is managed
// manually by the user.
//
// modelType ("llm" or "sd") forces the model kind; when empty the name/vae
// heuristic from isSDModel is used.
func AddOrReplaceModelInFile(path, name, modelPath, mmprojPath, vaePath, modelType string, tpl Templates) error {
	var sd bool
	switch modelType {
	case "sd":
		sd = true
	case "llm":
		sd = false
	default:
		sd = isSDModel(name, vaePath)
	}
	body := buildModelBody(name, modelPath, mmprojPath, vaePath, sd, tpl)

	lines, err := readConfigLines(path)
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		lines = defaultGlobalHeader()
	}
	lines = upsertModelEntry(lines, name, body, sd)
	return writeConfigLines(path, lines)
}

// buildModelBody renders the body of a model entry at 0-indent (cmd block
// scalar content at indent 2; the caller re-indents by 4 for the file).
func buildModelBody(name, modelPath, mmprojPath, vaePath string, sd bool, tpl Templates) string {
	var cmd string
	var ttl int
	if sd {
		cmd = ApplySDCmd(tpl, modelPath, vaePath)
		ttl = tpl.SDTtl
	} else {
		cmd = ApplyLLMCmd(tpl, modelPath, name, mmprojPath)
		ttl = LLMTtlFor(tpl, name)
	}
	var b strings.Builder
	b.WriteString("cmd: |\n")
	for _, line := range strings.Split(cmd, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("ttl: ")
	b.WriteString(strconv.Itoa(ttl))
	b.WriteByte('\n')
	if sd {
		checkEndpoint := tpl.SDCheckEndpoint
		if checkEndpoint == "" {
			checkEndpoint = DefaultSDCheckEndpoint
		}
		b.WriteString("checkEndpoint: ")
		b.WriteString(checkEndpoint)
		b.WriteByte('\n')
	}
	mType := "llm"
	if sd {
		mType = "sd"
	}
	b.WriteString("metadata:\n")
	b.WriteString("  model_type: ")
	b.WriteString(mType)
	b.WriteByte('\n')
	b.WriteString("  port: ${PORT}")
	return b.String()
}

// upsertModelEntry replaces (or inserts) the `  name:` block under the
// models: section. body is at 0-indent; it is re-indented to 4 spaces.
// New LLM entries (sd=false) are inserted at the top of the models list;
// new SD entries are appended at the bottom. Existing entries are replaced
// in place, preserving their position.
func upsertModelEntry(lines []string, name, body string, sd bool) []string {
	bodyLines := indentBlock(splitYamlLines(body), indentModelBody)
	header := strings.Repeat(" ", indentModelName) + name + ":"

	modelsStart, modelsEnd, ns, ne := findModelBlock(lines, name)
	if modelsStart == -1 {
		out := append([]string(nil), lines...)
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, "models:")
		out = append(out, header)
		out = append(out, bodyLines...)
		return out
	}
	if ns != -1 {
		out := make([]string, 0, len(lines)-(ne-ns-1)+len(bodyLines))
		out = append(out, lines[:ns+1]...)
		out = append(out, bodyLines...)
		out = append(out, lines[ne:]...)
		return out
	}
	addition := make([]string, 0, 1+len(bodyLines))
	addition = append(addition, header)
	addition = append(addition, bodyLines...)

	var insertAt int
	if sd {
		insertAt = modelsEnd
		for insertAt > modelsStart+1 && strings.TrimSpace(lines[insertAt-1]) == "" {
			insertAt--
		}
	} else {
		insertAt = modelsStart + 1
	}
	out := make([]string, 0, len(lines)+len(addition))
	out = append(out, lines[:insertAt]...)
	out = append(out, addition...)
	out = append(out, lines[insertAt:]...)
	return out
}

func defaultGlobalHeader() []string {
	return []string{
		"healthCheckTimeout: 300",
		"logLevel: info",
		"startPort: 5901",
		"logToStdout: both",
		"globalTTL: 600",
		"sendLoadingState: true",
	}
}

// ListModelsFromFile parses path with yaml.v3 only for the read path and
// returns all model entries with cmd-extracted paths. The file is not
// written to, so formatting is unaffected.
func ListModelsFromFile(path string) ([]ModelEntry, error) {
	doc, err := LoadFile(path)
	if err != nil {
		return nil, err
	}
	return ListModels(doc), nil
}
