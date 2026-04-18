package ini

import (
	"bytes"
	"strings"
	"testing"
)

const sampleINI = `; managed by w84ggufman
; do not edit manually

[global]
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

func TestParse(t *testing.T) {
	f, err := Parse(strings.NewReader(sampleINI))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(f.Header) != 2 {
		t.Errorf("want 2 header lines, got %d", len(f.Header))
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

	gi := strings.Index(out, "[global]")
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
	// Original sections still present
	if f2.Sections["AlphaModel"] == nil {
		t.Error("AlphaModel missing after adding NewModel")
	}
}

func TestRemoveSection(t *testing.T) {
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
