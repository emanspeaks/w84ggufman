package llamaswap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0664); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(data)
}

// Adding a new LLM entry must insert at the top of the models: section, leave
// comments untouched, and never modify groups.
func TestAddLLMModel_TopOfList_NoGroupEdit(t *testing.T) {
	initial := `# llama-swap config (comment preserved)
healthCheckTimeout: 300
logLevel: info  # inline comment

# models section
models:
  # existing
  foo:
    cmd: |
      llama-server
      -m /path/foo.gguf
    ttl: 600

groups:
  # hand-managed
  llm:
    swap: false
    members:
      - foo
`
	path := writeTempConfig(t, initial)
	tpl := DefaultTemplates()
	if err := AddOrReplaceModelInFile(path, "bar", "/models/bar.gguf", "", "", "llm", tpl); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := readFile(t, path)
	for _, marker := range []string{
		"# llama-swap config (comment preserved)",
		"# inline comment",
		"# models section",
		"# existing",
		"# hand-managed",
	} {
		if !strings.Contains(got, marker) {
			t.Errorf("comment %q lost; file:\n%s", marker, got)
		}
	}
	// bar: must come before foo: (inserted at top)
	barIdx := strings.Index(got, "  bar:")
	fooIdx := strings.Index(got, "  foo:")
	if barIdx == -1 || fooIdx == -1 || barIdx > fooIdx {
		t.Errorf("bar should be inserted before foo; file:\n%s", got)
	}
	// Groups section must be byte-identical.
	if !strings.Contains(got, "groups:\n  # hand-managed\n  llm:\n    swap: false\n    members:\n      - foo\n") {
		t.Errorf("groups section was modified; file:\n%s", got)
	}
}

// Adding a new SD entry must append at the bottom of models: and leave groups
// untouched.
func TestAddSDModel_BottomOfList_NoGroupEdit(t *testing.T) {
	initial := `models:
  foo:
    cmd: |
      llama-server
      -m /path/foo.gguf
    ttl: 600

groups:
  llm:
    members:
      - foo
`
	path := writeTempConfig(t, initial)
	tpl := DefaultTemplates()
	if err := AddOrReplaceModelInFile(path, "flux-dev", "/models/flux.gguf", "", "/vae.safetensors", "sd", tpl); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := readFile(t, path)
	barIdx := strings.Index(got, "  flux-dev:")
	fooIdx := strings.Index(got, "  foo:")
	if barIdx == -1 || fooIdx == -1 || barIdx < fooIdx {
		t.Errorf("flux-dev should be appended after foo; file:\n%s", got)
	}
	// Groups section unchanged.
	if !strings.Contains(got, "groups:\n  llm:\n    members:\n      - foo\n") {
		t.Errorf("groups section was modified; file:\n%s", got)
	}
	// No sd group was created.
	if strings.Contains(got, "sd:\n") && strings.Index(got, "sd:") > strings.Index(got, "groups:") {
		t.Errorf("sd group should not have been created; file:\n%s", got)
	}
}

// Removing a model must leave surrounding blocks, comments, and groups
// untouched.
func TestRemoveModel_PreservesComments_NoGroupEdit(t *testing.T) {
	initial := `# head comment
models:
  foo:
    cmd: |
      llama-server
      -m /path/foo.gguf
    ttl: 600
  # keep this one
  bar:
    cmd: |
      llama-server
      -m /path/bar.gguf
    ttl: 0

groups:
  llm:
    members:
      - foo
      - bar
`
	path := writeTempConfig(t, initial)
	if err := RemoveModelFromFile(path, "foo"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got := readFile(t, path)
	if strings.Contains(got, "foo:") && strings.Index(got, "foo:") < strings.Index(got, "groups:") {
		t.Errorf("foo entry not removed from models; file:\n%s", got)
	}
	if !strings.Contains(got, "# head comment") {
		t.Errorf("head comment lost; file:\n%s", got)
	}
	if !strings.Contains(got, "# keep this one") {
		t.Errorf("comment attached to next sibling lost; file:\n%s", got)
	}
	if !strings.Contains(got, "  bar:\n    cmd: |\n      llama-server\n      -m /path/bar.gguf\n    ttl: 0") {
		t.Errorf("bar entry altered; file:\n%s", got)
	}
	// Groups members are NOT auto-cleaned (user-managed).
	if !strings.Contains(got, "      - foo\n") {
		t.Errorf("groups member for foo should be preserved (user-managed); file:\n%s", got)
	}
}

// Round-tripping ReadModelRaw → WriteModelRaw with the same body must be a
// no-op on the file (byte-identical).
func TestWriteModelRaw_RoundTrip(t *testing.T) {
	initial := `models:
  foo:
    cmd: |
      llama-server
      -m /path/foo.gguf
    ttl: 600
    metadata:
      model_type: llm
      port: ${PORT}
  bar:
    cmd: |
      other
    ttl: 0
`
	path := writeTempConfig(t, initial)
	body, err := ReadModelRawFromFile(path, "foo")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := WriteModelRawToFile(path, "foo", body); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readFile(t, path)
	if got != initial {
		t.Errorf("round-trip not byte-identical\n--- want ---\n%s\n--- got ---\n%s", initial, got)
	}
}

// Replacing an existing model body must not disturb earlier or later entries.
func TestAddOrReplaceModel_ReplacesInPlace(t *testing.T) {
	initial := `models:
  alpha:
    cmd: |
      alpha-cmd
    ttl: 1
  foo:
    cmd: |
      old-cmd
    ttl: 99
  zeta:
    cmd: |
      zeta-cmd
    ttl: 2
`
	path := writeTempConfig(t, initial)
	tpl := DefaultTemplates()
	if err := AddOrReplaceModelInFile(path, "foo", "/new/foo.gguf", "", "", "llm", tpl); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "  alpha:\n    cmd: |\n      alpha-cmd\n    ttl: 1") {
		t.Errorf("alpha altered; file:\n%s", got)
	}
	if !strings.Contains(got, "  zeta:\n    cmd: |\n      zeta-cmd\n    ttl: 2") {
		t.Errorf("zeta altered; file:\n%s", got)
	}
	if strings.Contains(got, "old-cmd") {
		t.Errorf("old foo body still present; file:\n%s", got)
	}
	if !strings.Contains(got, "/new/foo.gguf") {
		t.Errorf("new foo body missing; file:\n%s", got)
	}
}
