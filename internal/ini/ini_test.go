package ini

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleINI = `; managed by w84ggufman

[*]
ctx-size = 65536
flash-attn = on
jinja = true
n-gpu-layers = 999

[AlphaModel]
ctx-size = 32768
mmproj = /models/AlphaModel/mmproj-F16.gguf
model = /models/AlphaModel/alpha.gguf

[ZetaModel]
model = /models/ZetaModel/zeta.gguf
`

// legacySampleINI uses the old [global] header to verify backwards-compat.
const legacySampleINI = `; managed by w84ggufman

[global]
ctx-size = 65536
n-gpu-layers = 999

[ZetaModel]
model = /models/ZetaModel/zeta.gguf
`

func TestParse(t *testing.T) {
	f, err := Parse(strings.NewReader(sampleINI))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(f.Header) != 1 {
		t.Errorf("want 1 header line, got %d", len(f.Header))
	}
	if f.Global["ctx-size"] != "65536" {
		t.Errorf("global ctx-size: got %q", f.Global["ctx-size"])
	}
	if f.Global["n-gpu-layers"] != "999" {
		t.Errorf("global n-gpu-layers: got %q", f.Global["n-gpu-layers"])
	}
	if len(f.Sections) != 2 {
		t.Errorf("want 2 sections, got %d", len(f.Sections))
	}
	if f.Sections["AlphaModel"]["mmproj"] != "/models/AlphaModel/mmproj-F16.gguf" {
		t.Errorf("AlphaModel mmproj: got %q", f.Sections["AlphaModel"]["mmproj"])
	}
	if f.Sections["ZetaModel"]["model"] != "/models/ZetaModel/zeta.gguf" {
		t.Errorf("ZetaModel model: got %q", f.Sections["ZetaModel"]["model"])
	}
}

func TestParseLegacyGlobal(t *testing.T) {
	f, err := Parse(strings.NewReader(legacySampleINI))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Global["ctx-size"] != "65536" {
		t.Errorf("[global] section not parsed as global; ctx-size = %q", f.Global["ctx-size"])
	}
	if len(f.Sections) != 1 {
		t.Errorf("want 1 section, got %d", len(f.Sections))
	}
}

func TestRoundTrip(t *testing.T) {
	f, err := Parse(strings.NewReader(sampleINI))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	f2, err := Parse(&buf)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	if f2.Global["ctx-size"] != f.Global["ctx-size"] {
		t.Error("round-trip: global ctx-size mismatch")
	}
	if len(f2.Sections) != len(f.Sections) {
		t.Errorf("round-trip: section count %d != %d", len(f2.Sections), len(f.Sections))
	}
	for name, sec := range f.Sections {
		for k, v := range sec {
			if f2.Sections[name][k] != v {
				t.Errorf("round-trip: [%s] %s = %q, want %q", name, k, f2.Sections[name][k], v)
			}
		}
	}
}

func TestGlobalFirst(t *testing.T) {
	f := New()
	f.Global["x"] = "1"
	f.Sections["AAA"] = map[string]string{"model": "/a"}
	f.Sections["ZZZ"] = map[string]string{"model": "/z"}

	var buf bytes.Buffer
	f.Write(&buf)
	out := buf.String()

	gi := strings.Index(out, "[*]")
	ai := strings.Index(out, "[AAA]")
	zi := strings.Index(out, "[ZZZ]")
	if gi < 0 || ai < 0 || zi < 0 {
		t.Fatal("missing section in output")
	}
	if !(gi < ai && ai < zi) {
		t.Errorf("wrong order: global=%d AAA=%d ZZZ=%d", gi, ai, zi)
	}
}

func TestMissingFileReturnsEmpty(t *testing.T) {
	f, err := ParseFile("/nonexistent/path/that/does/not/exist.ini")
	if err != nil {
		t.Fatalf("want nil error for missing file, got %v", err)
	}
	if len(f.Global) != 0 || len(f.Sections) != 0 {
		t.Error("expected empty file for missing path")
	}
}

func TestAddSection(t *testing.T) {
	f, _ := Parse(strings.NewReader(sampleINI))
	f.Sections["NewModel"] = map[string]string{"model": "/new/model.gguf"}

	var buf bytes.Buffer
	f.Write(&buf)
	f2, _ := Parse(&buf)

	if f2.Sections["NewModel"]["model"] != "/new/model.gguf" {
		t.Error("NewModel not found after adding")
	}
	if f2.Sections["AlphaModel"] == nil {
		t.Error("AlphaModel missing after adding NewModel")
	}
}

func TestWriteRemoveSection(t *testing.T) {
	f, _ := Parse(strings.NewReader(sampleINI))
	delete(f.Sections, "AlphaModel")

	var buf bytes.Buffer
	f.Write(&buf)
	out := buf.String()

	if strings.Contains(out, "AlphaModel") {
		t.Error("AlphaModel still present after removal")
	}
	if !strings.Contains(out, "ZetaModel") {
		t.Error("ZetaModel missing after removing AlphaModel")
	}
}

func TestEmptyFile(t *testing.T) {
	f := New()
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("Write empty: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output, got %q", buf.String())
	}
}

// ── Text-based surgical operations ───────────────────────────────────────────

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "managed.ini")
	if err := os.WriteFile(p, []byte(content), 0664); err != nil {
		t.Fatalf("writeTemp: %v", err)
	}
	return p
}

func readTemp(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readTemp: %v", err)
	}
	return string(b)
}

func TestRemoveSectionMiddle(t *testing.T) {
	p := writeTemp(t, sampleINI)
	if err := RemoveSection(p, "AlphaModel"); err != nil {
		t.Fatalf("RemoveSection: %v", err)
	}
	out := readTemp(t, p)
	if strings.Contains(out, "AlphaModel") {
		t.Error("AlphaModel still present")
	}
	if !strings.Contains(out, "ZetaModel") {
		t.Error("ZetaModel missing")
	}
	if !strings.Contains(out, "[*]") {
		t.Error("[*] global section missing")
	}
	// Comments must survive.
	if !strings.Contains(out, "; managed by w84ggufman") {
		t.Error("header comment missing")
	}
}

func TestRemoveSectionLast(t *testing.T) {
	p := writeTemp(t, sampleINI)
	if err := RemoveSection(p, "ZetaModel"); err != nil {
		t.Fatalf("RemoveSection: %v", err)
	}
	out := readTemp(t, p)
	if strings.Contains(out, "ZetaModel") {
		t.Error("ZetaModel still present")
	}
	if !strings.Contains(out, "AlphaModel") {
		t.Error("AlphaModel missing")
	}
}

func TestRemoveSectionNotFound(t *testing.T) {
	p := writeTemp(t, sampleINI)
	original := readTemp(t, p)
	if err := RemoveSection(p, "DoesNotExist"); err != nil {
		t.Fatalf("RemoveSection: %v", err)
	}
	if readTemp(t, p) != original {
		t.Error("file changed when section not found")
	}
}

func TestRemoveSectionMissingFile(t *testing.T) {
	err := RemoveSection(filepath.Join(t.TempDir(), "nope.ini"), "X")
	if err != nil {
		t.Errorf("expected nil for missing file, got %v", err)
	}
}

func TestAppendSection(t *testing.T) {
	p := writeTemp(t, sampleINI)
	kvs := map[string]string{"model": "/models/New/new.gguf", "mmproj": "/models/New/mmproj.gguf"}
	if err := AppendSection(p, "NewModel", kvs); err != nil {
		t.Fatalf("AppendSection: %v", err)
	}
	out := readTemp(t, p)
	if !strings.Contains(out, "[NewModel]") {
		t.Error("[NewModel] not found")
	}
	if !strings.Contains(out, "mmproj = /models/New/mmproj.gguf") {
		t.Error("mmproj key missing")
	}
	// Existing content must survive.
	if !strings.Contains(out, "AlphaModel") {
		t.Error("AlphaModel missing after append")
	}
	if !strings.Contains(out, "; managed by w84ggufman") {
		t.Error("header comment lost after append")
	}
}

func TestAppendSectionCreatesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new.ini")
	if err := AppendSection(p, "M", map[string]string{"model": "/m"}); err != nil {
		t.Fatalf("AppendSection: %v", err)
	}
	out := readTemp(t, p)
	if !strings.Contains(out, "[M]") {
		t.Error("[M] not found in created file")
	}
}

func TestUpsertSectionKeysUpdateExisting(t *testing.T) {
	p := writeTemp(t, sampleINI)
	if err := UpsertSectionKeys(p, "*", map[string]string{"ctx-size": "131072"}); err != nil {
		t.Fatalf("UpsertSectionKeys: %v", err)
	}
	out := readTemp(t, p)
	if !strings.Contains(out, "ctx-size = 131072") {
		t.Error("ctx-size not updated")
	}
	// Other global keys must survive.
	if !strings.Contains(out, "n-gpu-layers = 999") {
		t.Error("n-gpu-layers lost")
	}
	// Model sections must survive.
	if !strings.Contains(out, "AlphaModel") {
		t.Error("AlphaModel lost after upsert")
	}
	if !strings.Contains(out, "; managed by w84ggufman") {
		t.Error("header comment lost")
	}
}

func TestUpsertSectionKeysAddNew(t *testing.T) {
	p := writeTemp(t, sampleINI)
	if err := UpsertSectionKeys(p, "*", map[string]string{"threads": "8"}); err != nil {
		t.Fatalf("UpsertSectionKeys: %v", err)
	}
	out := readTemp(t, p)
	if !strings.Contains(out, "threads = 8") {
		t.Error("new key not added")
	}
	if !strings.Contains(out, "ctx-size = 65536") {
		t.Error("existing key lost")
	}
}

func TestUpsertSectionKeysLegacyGlobal(t *testing.T) {
	// Files with [global] header should still be updated when we pass "*".
	p := writeTemp(t, legacySampleINI)
	if err := UpsertSectionKeys(p, "*", map[string]string{"ctx-size": "4096"}); err != nil {
		t.Fatalf("UpsertSectionKeys: %v", err)
	}
	out := readTemp(t, p)
	if !strings.Contains(out, "ctx-size = 4096") {
		t.Error("ctx-size not updated in legacy [global] file")
	}
}

func TestUpsertSectionKeysSectionNotFound(t *testing.T) {
	p := writeTemp(t, sampleINI)
	if err := UpsertSectionKeys(p, "BrandNewModel", map[string]string{"model": "/x"}); err != nil {
		t.Fatalf("UpsertSectionKeys: %v", err)
	}
	out := readTemp(t, p)
	if !strings.Contains(out, "[BrandNewModel]") {
		t.Error("section not appended when missing")
	}
}

func TestUpsertSectionKeysModelSection(t *testing.T) {
	p := writeTemp(t, sampleINI)
	if err := UpsertSectionKeys(p, "AlphaModel", map[string]string{"ctx-size": "8192", "threads": "4"}); err != nil {
		t.Fatalf("UpsertSectionKeys: %v", err)
	}
	out := readTemp(t, p)
	if !strings.Contains(out, "ctx-size = 8192") {
		t.Error("ctx-size not updated in model section")
	}
	if !strings.Contains(out, "threads = 4") {
		t.Error("new key not added to model section")
	}
	// Other keys in the section must survive.
	if !strings.Contains(out, "mmproj = /models/AlphaModel/mmproj-F16.gguf") {
		t.Error("mmproj lost from AlphaModel")
	}
}
