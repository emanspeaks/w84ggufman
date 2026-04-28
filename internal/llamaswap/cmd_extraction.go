package llamaswap

import (
	"regexp"
	"strconv"
	"strings"
)

func normalizeCmdForParsing(cmd string) string {
	cmd = stripShellLineComments(cmd)
	cmd = strings.ReplaceAll(cmd, "\\\n", " ")
	cmd = strings.ReplaceAll(cmd, "\\\r\n", " ")
	return cmd
}

// stripShellLineComments removes shell-style # comments while preserving text
// inside single and double quotes.
func stripShellLineComments(cmd string) string {
	var b strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	atLineStart := true
	prevWasSpace := true

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]

		if ch == '\n' {
			b.WriteByte(ch)
			inSingle = false
			inDouble = false
			escaped = false
			atLineStart = true
			prevWasSpace = true
			continue
		}

		if inSingle {
			b.WriteByte(ch)
			if ch == '\'' {
				inSingle = false
			}
			atLineStart = false
			prevWasSpace = ch == ' ' || ch == '\t'
			continue
		}

		if inDouble {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inDouble = false
			}
			atLineStart = false
			prevWasSpace = ch == ' ' || ch == '\t'
			continue
		}

		if ch == '#' && (atLineStart || prevWasSpace) {
			for i+1 < len(cmd) && cmd[i+1] != '\n' {
				i++
			}
			continue
		}

		b.WriteByte(ch)
		switch ch {
		case '\'':
			inSingle = true
			atLineStart = false
			prevWasSpace = false
		case '"':
			inDouble = true
			atLineStart = false
			prevWasSpace = false
		case ' ', '\t', '\r':
			if atLineStart {
				atLineStart = true
			}
			prevWasSpace = true
		default:
			atLineStart = false
			prevWasSpace = false
		}
	}

	return b.String()
}

// extractCmdPaths returns every value associated with any flag in cmd.
// It collects both "--flag value" and "--flag=value" forms without requiring
// a specific allowlist of flag names, so new flags like --llm are covered
// automatically. Non-path values (port numbers, counts, etc.) are harmless
// because the caller filters by whether the path is under the models dir.
func extractCmdPaths(cmd string) []string {
	cmd = normalizeCmdForParsing(cmd)
	tokens := strings.Fields(cmd)
	out := make([]string, 0)
	seen := make(map[string]struct{})
	add := func(p string) {
		p = strings.TrimSpace(strings.Trim(p, `"'`))
		if p == "" || strings.HasPrefix(p, "-") {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for i, t := range tokens {
		if !strings.HasPrefix(t, "-") {
			continue
		}
		if idx := strings.Index(t, "="); idx != -1 {
			add(t[idx+1:])
		} else if i+1 < len(tokens) {
			add(tokens[i+1])
		}
	}
	return out
}

// extractCmdFlag finds the value after flag in a cmd string. Handles
// newline-separated, backslash-continuation, and single-line formats.
func extractCmdFlag(cmd, flag string) string {
	cmd = normalizeCmdForParsing(cmd)
	tokens := strings.Fields(cmd)
	for i, t := range tokens {
		if strings.HasPrefix(t, flag+"=") {
			return strings.TrimPrefix(t, flag+"=")
		}
		if t == flag && i+1 < len(tokens) {
			return tokens[i+1]
		}
	}
	return ""
}

// --- model classification and command builders ---

// paramCountRe matches a parameter count like "3B", "70B", "0.5B" in a model name.
var paramCountRe = regexp.MustCompile(`(?i)[-_](\d+(?:\.\d+)?)B(?:[-_.]|$)`)

func parseBillionParams(name string) float64 {
	if m := paramCountRe.FindStringSubmatch(name); m != nil {
		v, _ := strconv.ParseFloat(m[1], 64)
		return v
	}
	return 0
}

// isSDModel returns true for Stable Diffusion / Flux image generation models.
// A non-empty vaePath is definitive; otherwise the name prefix is used as a
// heuristic (llama-swap convention: SD models are named "flux-*" or "sd-*").
func isSDModel(name, vaePath string) bool {
	if vaePath != "" {
		return true
	}
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "flux-") || strings.HasPrefix(lower, "sd-")
}

// llmTTL returns the recommended TTL for an LLM based on its parameter count.
// Models under 10B get 600 s; larger or unknown models get 0 (never evict).
func llmTTL(name string) int {
	b := parseBillionParams(name)
	if b > 0 && b < 10 {
		return 600
	}
	return 0
}
