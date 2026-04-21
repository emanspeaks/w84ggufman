package ini

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

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

	// Walk back over blank lines immediately before [section] header.
	blankStart := start
	for blankStart > 0 && strings.TrimSpace(lines[blankStart-1]) == "" {
		blankStart--
	}
	// When content exists on both sides of the removed block, preserve exactly
	// one blank separator line; otherwise consume all leading blank lines.
	if blankStart > 0 && end < len(lines) {
		start = blankStart + 1 // keep one blank (at blankStart) as separator
	} else {
		start = blankStart
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
	switch section {
	case "*":
		headers = append(headers, "[global]")
	case "global":
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

// ReadSectionRaw returns the raw body of the named section — every line
// between the section header and the next section header — exactly as it
// appears in the file, including inline comments and blank lines.
// Leading and trailing blank lines within the body are trimmed.
// Returns an empty string (no error) if the section is not found or the
// file does not exist.
func ReadSectionRaw(path, section string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(src, "\n")
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
		return "", nil
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			end = i
			break
		}
	}

	body := lines[start+1 : end]
	for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
		body = body[1:]
	}
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	if len(body) == 0 {
		return "", nil
	}
	return strings.Join(body, "\n"), nil
}

// ReplaceSectionBody replaces the body of the named section (everything
// between its header line and the next section) with newBody, preserving
// the section's position in the file. Other sections and all comments
// outside the target section are left unchanged.
// If the section does not exist it is appended. If the file does not
// exist it is created.
func ReplaceSectionBody(path, section, newBody string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			f, err2 := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0664)
			if err2 != nil {
				return err2
			}
			defer f.Close()
			bw := bufio.NewWriter(f)
			fmt.Fprintf(bw, "[%s]\n%s\n", section, newBody)
			return bw.Flush()
		}
		return err
	}

	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(src, "\n")
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
		// Append new section.
		f, err2 := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0664)
		if err2 != nil {
			return err2
		}
		defer f.Close()
		bw := bufio.NewWriter(f)
		fmt.Fprintf(bw, "\n[%s]\n%s\n", section, newBody)
		return bw.Flush()
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			end = i
			break
		}
	}

	bodyLines := strings.Split(strings.ReplaceAll(newBody, "\r\n", "\n"), "\n")
	out := make([]string, 0, start+1+len(bodyLines)+(len(lines)-end))
	out = append(out, lines[:start+1]...) // keep [section] header
	out = append(out, bodyLines...)
	out = append(out, lines[end:]...)

	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0664)
}
