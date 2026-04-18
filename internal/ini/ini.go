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
// Global holds the [global] section. Sections holds all other sections.
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

// Parse reads an INI from r.
func Parse(r io.Reader) (*File, error) {
	out := New()
	scanner := bufio.NewScanner(r)
	var current string // current section name; "" means global
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
			if current != "global" {
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
		if current == "global" || current == "" {
			out.Global[key] = val
		} else {
			out.Sections[current][key] = val
		}
	}
	return out, scanner.Err()
}

// Write serialises f to w. [global] is always written first; remaining
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
		fmt.Fprintln(bw, "[global]")
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
