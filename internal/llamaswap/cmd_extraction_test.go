package llamaswap

import (
	"reflect"
	"testing"
)

func TestExtractCmdPathsIgnoresCommentedSegments(t *testing.T) {
	cmd := `llama-server \
  -m /models/repo/active.gguf \
  --mmproj /models/repo/mmproj.gguf # --mmproj /models/repo/old-mmproj.gguf
  # --vae /models/repo/commented-out.vae
  --vae /models/repo/active.vae`

	got := extractCmdPaths(cmd)
	want := []string{
		"/models/repo/active.gguf",
		"/models/repo/mmproj.gguf",
		"/models/repo/active.vae",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractCmdPaths mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestExtractCmdFlagIgnoresCommentedFlagAndSupportsEquals(t *testing.T) {
	cmd := `llama-server --mmproj=/models/repo/new-mmproj.gguf # --mmproj /models/repo/old-mmproj.gguf`
	got := extractCmdFlag(cmd, "--mmproj")
	want := "/models/repo/new-mmproj.gguf"
	if got != want {
		t.Fatalf("extractCmdFlag mismatch: got %q want %q", got, want)
	}
}

func TestStripShellLineCommentsKeepsQuotedHashes(t *testing.T) {
	cmd := `llama-server -m "/models/repo/#literal.gguf" # trailing comment`
	got := extractCmdFlag(cmd, "-m")
	want := `"/models/repo/#literal.gguf"`
	if got != want {
		t.Fatalf("quoted hash should be preserved: got %q want %q", got, want)
	}
}
