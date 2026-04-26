package api

import (
	"bytes"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var mdRenderer = goldmark.New(goldmark.WithExtensions(extension.GFM))
var readmeAttrURLRe = regexp.MustCompile(`(?i)\b(src|href)\s*=\s*("[^"]*"|'[^']*')`)

func (s *Server) HandleReadme(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("id")
	if repoID == "" {
		http.Error(w, "missing id parameter", http.StatusBadRequest)
		return
	}
	if strings.Count(repoID, "/") != 1 || strings.Contains(repoID, "..") || strings.ContainsAny(repoID, " \t\n") {
		http.Error(w, "invalid repo id", http.StatusBadRequest)
		return
	}
	url := "https://huggingface.co/" + repoID + "/resolve/main/README.md"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if s.cfg.HFToken != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(s.cfg.HFToken))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "failed to fetch readme: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "HuggingFace returned non-OK status", http.StatusBadGateway)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		http.Error(w, "failed to read readme", http.StatusInternalServerError)
		return
	}
	raw = stripFrontmatter(raw)
	var buf bytes.Buffer
	if err := mdRenderer.Convert(raw, &buf); err != nil {
		http.Error(w, "failed to render readme", http.StatusInternalServerError)
		return
	}
	html := rewriteReadmeURLs(buf.String(), repoID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

func rewriteReadmeURLs(htmlBody, repoID string) string {
	return readmeAttrURLRe.ReplaceAllStringFunc(htmlBody, func(attr string) string {
		m := readmeAttrURLRe.FindStringSubmatch(attr)
		if len(m) < 3 {
			return attr
		}
		name := strings.ToLower(m[1])
		quoted := m[2]
		if len(quoted) < 2 {
			return attr
		}
		quote := quoted[:1]
		rawURL := quoted[1 : len(quoted)-1]
		resolved := resolveReadmeURL(rawURL, repoID, name == "src")
		if resolved == rawURL {
			return attr
		}
		return name + "=" + quote + resolved + quote
	})
}

func resolveReadmeURL(rawURL, repoID string, isImage bool) string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return rawURL
	}
	lower := strings.ToLower(u)
	if strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "//") ||
		strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "javascript:") ||
		strings.HasPrefix(u, "#") {
		return rawURL
	}

	if strings.HasPrefix(u, "/") {
		return "https://huggingface.co" + u
	}

	pathPart, suffix := splitURLSuffix(u)
	cleaned := strings.TrimPrefix(path.Clean("/"+pathPart), "/")
	if cleaned == "" || cleaned == "." {
		return rawURL
	}

	if isImage {
		return "https://huggingface.co/" + repoID + "/resolve/main/" + cleaned + suffix
	}
	return "https://huggingface.co/" + repoID + "/blob/main/" + cleaned + suffix
}

func splitURLSuffix(u string) (string, string) {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		return u[:i], u[i:]
	}
	return u, ""
}

func stripFrontmatter(b []byte) []byte {
	if !bytes.HasPrefix(b, []byte("---")) {
		return b
	}
	end := bytes.Index(b[3:], []byte("\n---"))
	if end < 0 {
		return b
	}
	rest := b[3+end+4:]
	return bytes.TrimLeft(rest, "\n")
}

func (s *Server) HandleRepo(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("id")
	if repoID == "" {
		http.Error(w, "missing id parameter", http.StatusBadRequest)
		return
	}
	repoInfo, err := s.deps.FetchRepoInfo(repoID, s.cfg.HFToken)
	if err != nil {
		http.Error(w, "failed to fetch repo: "+err.Error(), http.StatusBadGateway)
		return
	}
	repoDir := filepath.Join(s.cfg.ModelsDir, filepath.FromSlash(repoID))
	localFiles, _ := s.deps.ScanFilesRelative(repoDir)
	localFiles = s.deps.FilterIgnoredRelativeFiles(repoDir, localFiles, s.cfg)
	repoInfo.PresentFiles, repoInfo.RogueFiles = matchLocalToHF(localFiles, repoInfo)
	writeJSON(w, repoInfo)
}

var shardRe = regexp.MustCompile(`-\d{5}-of-\d{5}\.gguf$`)

func matchLocalToHF(localFiles []string, info *RepoInfo) (present []string, rogue []string) {
	hfBasenames := make(map[string]struct{})
	for _, f := range info.Models {
		hfBasenames[filepath.Base(f.Filename)] = struct{}{}
	}
	for _, f := range info.Sidecars {
		hfBasenames[filepath.Base(f.Filename)] = struct{}{}
	}

	for _, lf := range localFiles {
		base := filepath.Base(lf)
		if _, ok := hfBasenames[base]; ok {
			present = append(present, lf)
			continue
		}
		stemmed := shardRe.ReplaceAllString(base, ".gguf")
		stemBase := strings.TrimSuffix(stemmed, ".gguf")
		matched := false
		if stemBase != "" && stemBase != strings.TrimSuffix(base, ".gguf") {
			for k := range hfBasenames {
				if strings.TrimSuffix(shardRe.ReplaceAllString(k, ".gguf"), ".gguf") == stemBase {
					matched = true
					break
				}
			}
		}
		if matched {
			present = append(present, lf)
		} else {
			rogue = append(rogue, lf)
		}
	}
	return
}

func (s *Server) HandleLocalFiles(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" || strings.Contains(id, "..") {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	var repoDir string
	if filepath.IsAbs(id) {
		clean := filepath.Clean(id)
		if !strings.HasPrefix(clean, filepath.Clean(s.cfg.ModelsDir)+string(filepath.Separator)) {
			http.Error(w, "path not under models dir", http.StatusBadRequest)
			return
		}
		repoDir = clean
	} else {
		repoDir = filepath.Join(s.cfg.ModelsDir, filepath.FromSlash(id))
	}
	files, _ := s.deps.ScanFilesRelative(repoDir)
	files = s.deps.FilterIgnoredRelativeFiles(repoDir, files, s.cfg)
	meta := s.deps.ReadModelMeta(repoDir)
	hasUpdate := s.deps.HasUpdateAvailable != nil && s.deps.HasUpdateAvailable(repoDir)
	writeJSON(w, &RepoInfo{LocalOnly: true, RogueFiles: files, HasUpdate: hasUpdate, RepoID: meta.RepoID})
}
