package main

import (
	"path/filepath"
	"strings"
)

// defaultIgnorePatterns are the patterns applied to top-level modelsDir entries
// unless overridden by a .w84ggufman.json at the modelsDir root.
var defaultIgnorePatterns = []string{".cache", ".w84ggufman*"}

// matchesIgnorePattern reports whether name (a single path component) matches a
// preprocessed gitignore-style pattern. isDir indicates whether name refers to a
// directory; trailing-slash and "/**" patterns only match directories. The caller
// must have already stripped leading "!" negation and backslash escapes.
func matchesIgnorePattern(name, pattern string, isDir bool) bool {
	// Trailing "/" -> directory-only match.
	if strings.HasSuffix(pattern, "/") {
		if !isDir {
			return false
		}
		pattern = strings.TrimSuffix(pattern, "/")
	}

	// Strip a leading "/" (anchors to .gitignore root; irrelevant for name-level matching).
	pattern = strings.TrimPrefix(pattern, "/")

	// "**/name" -> match at any depth; strip the prefix.
	pattern = strings.TrimPrefix(pattern, "**/")

	// "name/**" -> matches everything inside name; treat as dir-only match on name.
	if strings.HasSuffix(pattern, "/**") {
		if !isDir {
			return false
		}
		pattern = strings.TrimSuffix(pattern, "/**")
	}

	// Collapse any remaining "**" -> "*" (no path separators in a single name).
	pattern = strings.ReplaceAll(pattern, "**", "*")

	matched, _ := filepath.Match(pattern, name)
	return matched
}

// isIgnoredEntry reports whether an entry should be excluded. Patterns are
// evaluated in gitignore order: later patterns override earlier ones. Blank
// lines and "#"-prefixed lines are comments; "\#" and "\!" escape the
// respective special chars. "!" negates (whitelists) a previous match.
// Trailing unescaped whitespace is trimmed. The dot-files default acts as
// the implicit first rule and can be overridden by a later "!.name" pattern.
func isIgnoredEntry(name string, patterns []string, showDotFiles, isDir bool) bool {
	ignored := !showDotFiles && strings.HasPrefix(name, ".")
	for _, raw := range patterns {
		// Trim trailing unescaped whitespace.
		p := strings.TrimRight(raw, " \t")
		if len(p) < len(raw) && strings.HasSuffix(p, "\\") {
			p = p + " " // backslash-escaped trailing space is significant
		}
		// Skip blank lines and comments.
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		// Check for negation or leading backslash escape.
		negate := false
		if strings.HasPrefix(p, "!") {
			negate = true
			p = p[1:]
		} else if strings.HasPrefix(p, "\\") {
			p = p[1:] // \# or \! -> literal character
		}
		if p == "" {
			continue
		}
		if matchesIgnorePattern(name, p, isDir) {
			if negate {
				ignored = false
			} else {
				ignored = true
			}
		}
	}
	return ignored
}

func effectiveRootIgnorePatterns(cfg Config) []string {
	patterns := cfg.RootIgnorePatterns
	if len(patterns) == 0 {
		patterns = defaultIgnorePatterns
	}
	return patterns
}

// isIgnoredAbsolutePath evaluates ignore rules for an absolute path under
// modelsDir, merging ignore patterns recursively from modelsDir downward.
// Deeper directories append rules and can override parents with negation.
func isIgnoredAbsolutePath(path string, cfg Config, dirIgnoreCache map[string][]string) bool {
	modelsRoot := filepath.Clean(cfg.ModelsDir)
	cleanPath := filepath.Clean(path)
	sep := string(filepath.Separator)
	if cleanPath != modelsRoot && !strings.HasPrefix(cleanPath, modelsRoot+sep) {
		return false
	}

	rel, err := filepath.Rel(modelsRoot, cleanPath)
	if err != nil || rel == "." {
		return false
	}

	parts := strings.Split(filepath.ToSlash(rel), "/")
	patterns := append([]string(nil), effectiveRootIgnorePatterns(cfg)...)
	curDir := modelsRoot
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i > 0 {
			if cached, ok := dirIgnoreCache[curDir]; ok {
				patterns = append(patterns, cached...)
			} else {
				meta := readModelMeta(curDir)
				dirIgnoreCache[curDir] = append([]string(nil), meta.Ignore...)
				patterns = append(patterns, meta.Ignore...)
			}
		}
		isDir := i < len(parts)-1
		if isIgnoredEntry(p, patterns, cfg.ShowDotFiles, isDir) {
			return true
		}
		if isDir {
			curDir = filepath.Join(curDir, filepath.FromSlash(p))
		}
	}
	return false
}

func filterIgnoredRelativeFiles(baseDir string, files []string, cfg Config) []string {
	if len(files) == 0 {
		return files
	}
	filtered := make([]string, 0, len(files))
	dirIgnoreCache := make(map[string][]string)
	for _, f := range files {
		absPath := filepath.Join(baseDir, filepath.FromSlash(f))
		if isIgnoredAbsolutePath(absPath, cfg, dirIgnoreCache) {
			continue
		}
		filtered = append(filtered, f)
	}
	return filtered
}
