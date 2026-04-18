package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// detectRepoIDFromGGUF tries to determine the HuggingFace source repo ID by
// reading the metadata header of a GGUF file in the model directory.
// Returns "" when nothing useful is found.
func detectRepoIDFromGGUF(modelDir string, files []string) string {
	for _, f := range files {
		if matchesMmproj(f) || !strings.HasSuffix(f, ".gguf") {
			continue
		}
		meta, err := readGGUFMeta(filepath.Join(modelDir, f))
		if err != nil {
			continue
		}
		// Most specific field first — set by llama.cpp / HF quantization tools.
		if repo := meta["general.source.huggingface.repository"]; repo != "" {
			return repo
		}
		// Fall back to parsing any HuggingFace URL in the source / url fields.
		for _, key := range []string{"general.source.url", "general.url"} {
			if repo := repoFromHFURL(meta[key]); repo != "" {
				return repo
			}
		}
	}
	return ""
}

func repoFromHFURL(u string) string {
	u = strings.TrimPrefix(u, "https://huggingface.co/")
	u = strings.TrimPrefix(u, "http://huggingface.co/")
	// Must have exactly owner/name at the start; ignore anything deeper.
	parts := strings.SplitN(u, "/", 3)
	if len(parts) >= 2 && parts[0] != "" && parts[1] != "" && !strings.Contains(parts[0], ".") {
		return parts[0] + "/" + parts[1]
	}
	return ""
}

// readGGUFMeta reads the key-value metadata section from a GGUF file header
// and returns all string-valued keys. Non-string values are consumed and
// discarded. Any read error causes an early return of whatever was collected.
func readGGUFMeta(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return nil, err
	}
	if string(magic[:]) != "GGUF" {
		return nil, fmt.Errorf("not a GGUF file")
	}

	var version uint32
	if err := binary.Read(f, binary.LittleEndian, &version); err != nil {
		return nil, err
	}

	// v1 used uint32 for counts; v2+ use uint64.
	var kvCount uint64
	if version == 1 {
		var tc, kc uint32
		binary.Read(f, binary.LittleEndian, &tc) //nolint:errcheck
		binary.Read(f, binary.LittleEndian, &kc) //nolint:errcheck
		kvCount = uint64(kc)
	} else {
		var tc uint64
		binary.Read(f, binary.LittleEndian, &tc) //nolint:errcheck
		binary.Read(f, binary.LittleEndian, &kvCount) //nolint:errcheck
	}

	meta := make(map[string]string)
	for i := uint64(0); i < kvCount; i++ {
		key, err := ggufReadString(f)
		if err != nil {
			return meta, nil
		}
		var vtype uint32
		if err := binary.Read(f, binary.LittleEndian, &vtype); err != nil {
			return meta, nil
		}
		val, err := ggufConsumeValue(f, vtype)
		if err != nil {
			return meta, nil
		}
		if val != "" {
			meta[key] = val
		}
	}
	return meta, nil
}

// ggufReadString reads a GGUF length-prefixed UTF-8 string.
func ggufReadString(r io.Reader) (string, error) {
	var length uint64
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return "", err
	}
	if length == 0 {
		return "", nil
	}
	const maxString = 8192
	if length > maxString {
		_, err := io.CopyN(io.Discard, r, int64(length))
		return "", err
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// ggufConsumeValue reads and discards a typed GGUF value, returning it as a
// string only for string types (type 8).
func ggufConsumeValue(r io.Reader, vtype uint32) (string, error) {
	switch vtype {
	case 0, 7: // uint8, bool
		var v uint8
		return "", binary.Read(r, binary.LittleEndian, &v)
	case 1: // int8
		var v int8
		return "", binary.Read(r, binary.LittleEndian, &v)
	case 2: // uint16
		var v uint16
		return "", binary.Read(r, binary.LittleEndian, &v)
	case 3: // int16
		var v int16
		return "", binary.Read(r, binary.LittleEndian, &v)
	case 4: // uint32
		var v uint32
		return "", binary.Read(r, binary.LittleEndian, &v)
	case 5: // int32
		var v int32
		return "", binary.Read(r, binary.LittleEndian, &v)
	case 6: // float32
		var v float32
		return "", binary.Read(r, binary.LittleEndian, &v)
	case 8: // string
		s, err := ggufReadString(r)
		return s, err
	case 9: // array
		return "", ggufSkipArray(r)
	case 10: // uint64
		var v uint64
		return "", binary.Read(r, binary.LittleEndian, &v)
	case 11: // int64
		var v int64
		return "", binary.Read(r, binary.LittleEndian, &v)
	case 12: // float64
		var v float64
		return "", binary.Read(r, binary.LittleEndian, &v)
	default:
		return "", fmt.Errorf("unknown GGUF value type %d", vtype)
	}
}

func ggufSkipArray(r io.Reader) error {
	var arrayType uint32
	var count uint64
	if err := binary.Read(r, binary.LittleEndian, &arrayType); err != nil {
		return err
	}
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
		return err
	}
	for i := uint64(0); i < count; i++ {
		if _, err := ggufConsumeValue(r, arrayType); err != nil {
			return err
		}
	}
	return nil
}
