package ini

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// File represents a parsed INI file.
// Global holds the [*] (or [global]) section. Sections holds all other sections.
// Header holds comment lines (starting with ; or #) found before the first
// section or key-value pair.
type File struct {
	Header   []string
	Global   map[string]string
	Sections map[string]map[string]string
}

// New returns an empty File.
func New() *File {
	return &File{
		Global:   make(map[string]string),
		Sections: make(map[string]map[string]string),
	}
}

// ParseFile reads path and parses it. If the file does not exist it returns
// an empty File without error.
func ParseFile(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse reads an INI from r. Both [*] and [global] are treated as the
// global/wildcard section (key-values go into File.Global).
func Parse(r io.Reader) (*File, error) {
	out := New()
	scanner := bufio.NewScanner(r)
	var current string // current section name; "" or "*" or "global" means global
	headerDone := false

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			continue
		}

		// Collect header comments before the first meaningful line.
		if !headerDone && (strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#")) {
			out.Header = append(out.Header, line)
			continue
		}
		headerDone = true

		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			current = trimmed[1 : len(trimmed)-1]
			if current != "global" && current != "*" {
				if _, ok := out.Sections[current]; !ok {
					out.Sections[current] = make(map[string]string)
				}
			}
			continue
		}

		idx := strings.IndexByte(trimmed, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:idx])
		val := strings.TrimSpace(trimmed[idx+1:])
		if current == "global" || current == "*" || current == "" {
			out.Global[key] = val
		} else {
			out.Sections[current][key] = val
		}
	}
	return out, scanner.Err()
}

// Write serialises f to w. [*] is always written first; remaining
// sections are written in alphabetical order. Keys within each section are
// written in alphabetical order.
func (f *File) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)

	for _, h := range f.Header {
		if _, err := fmt.Fprintln(bw, h); err != nil {
			return err
		}
	}

	first := true

	if len(f.Global) > 0 {
		if len(f.Header) > 0 {
			fmt.Fprintln(bw)
		}
		fmt.Fprintln(bw, "[*]")
		for _, k := range sortedKeys(f.Global) {
			fmt.Fprintf(bw, "%s = %s\n", k, f.Global[k])
		}
		first = false
	}

	names := sortedKeys2(f.Sections)
	for _, name := range names {
		if !first {
			fmt.Fprintln(bw)
		}
		fmt.Fprintf(bw, "[%s]\n", name)
		for _, k := range sortedKeys(f.Sections[name]) {
			fmt.Fprintf(bw, "%s = %s\n", k, f.Sections[name][k])
		}
		first = false
	}

	return bw.Flush()
}

// WriteFile writes f to path, creating or truncating the file.
func (f *File) WriteFile(path string) error {
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	return f.Write(fh)
}

// RemoveSection removes the named section block (header line through last
// key-value line) from the file at path, along with any blank separator
// lines immediately preceding the header. If the section is not present,
// the file is left unchanged. If the file does not exist, RemoveSection
// returns nil.
func RemoveSection(path, section string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(src, "\n")
	// Drop the spurious empty string Split produces for a trailing newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	header := "[" + section + "]"
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == header {
			start = i
			break
		}
	}
	if start == -1 {
		return nil // not found; nothing to do
	}

	// Find the first line of the next section (or EOF).
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			end = i
			break
		}
	}

	// Also consume blank lines immediately before [section] header.
	for start > 0 && strings.TrimSpace(lines[start-1]) == "" {
		start--
	}

	out := make([]string, 0, len(lines)-(end-start))
	out = append(out, lines[:start]...)
	out = append(out, lines[end:]...)

	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0664)
}

// AppendSection appends a new [name] section with the supplied key-value
// pairs to the end of the file at path. The file is created (mode 0664)
// if it does not yet exist. Keys are written in alphabetical order.
func AppendSection(path, name string, kvs map[string]string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0664)
	if err != nil {
		return err
	}
	defer f.Close()

	bw := bufio.NewWriter(f)
	fmt.Fprintf(bw, "\n[%s]\n", name)
	for _, k := range sortedKeys(kvs) {
		fmt.Fprintf(bw, "%s = %s\n", k, kvs[k])
	}
	return bw.Flush()
}

// UpsertSectionKeys updates key-value pairs within the named section of the
// file at path. Existing keys have their values replaced in-place; keys that
// are absent in the section are appended at the end of the section. All
// content outside the section (including comments in other sections) is
// preserved exactly. If the section is not present, a new section is appended
// via AppendSection. If the file does not exist it is created.
func UpsertSectionKeys(path, section string, kvs map[string]string) error {
	if len(kvs) == 0 {
		return nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AppendSection(path, section, kvs)
		}
		return err
	}

	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(src, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Accept [*] and [global] as aliases for the global section header.
	headers := []string{"[" + section + "]"}
	if section == "*" {
		headers = append(headers, "[global]")
	} else if section == "global" {
		headers = append(headers, "[*]")
	}

	sectionStart := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		for _, h := range headers {
			if t == h {
				sectionStart = i
				break
			}
		}
		if sectionStart >= 0 {
			break
		}
	}
	if sectionStart == -1 {
		return AppendSection(path, section, kvs)
	}

	// Find end of section.
	sectionEnd := len(lines)
	for i := sectionStart + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			sectionEnd = i
			break
		}
	}

	// Walk through section lines replacing existing keys in-place.
	remaining := make(map[string]string, len(kvs))
	for k, v := range kvs {
		remaining[k] = v
	}

	updated := make([]string, len(lines))
	copy(updated, lines)

	for i := sectionStart + 1; i < sectionEnd; i++ {
		t := strings.TrimSpace(updated[i])
		idx := strings.IndexByte(t, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(t[:idx])
		if newVal, ok := remaining[key]; ok {
			updated[i] = key + " = " + newVal
			delete(remaining, key)
		}
	}

	if len(remaining) == 0 {
		return os.WriteFile(path, []byte(strings.Join(updated, "\n")+"\n"), 0664)
	}

	// Insert new keys just before the end of the section, after trimming any
	// trailing blank lines within the section body.
	insertAt := sectionEnd
	for insertAt > sectionStart+1 && strings.TrimSpace(updated[insertAt-1]) == "" {
		insertAt--
	}

	additions := make([]string, 0, len(remaining))
	for _, k := range sortedKeys(remaining) {
		additions = append(additions, k+" = "+remaining[k])
	}

	out := make([]string, 0, len(updated)+len(additions))
	out = append(out, updated[:insertAt]...)
	out = append(out, additions...)
	out = append(out, updated[insertAt:]...)

	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0664)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeys2(m map[string]map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
