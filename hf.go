package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

type HFFile struct {
	Filename    string `json:"filename"`
	Size        *int64 `json:"size"`
	DownloadURL string `json:"downloadURL"`
	DisplayName string `json:"displayName,omitempty"`
}

type HFRepoInfo struct {
	IsVision    bool     `json:"isVision"`
	Models      []HFFile `json:"models"`
	Sidecars    []HFFile `json:"sidecars"`
	PipelineTag string   `json:"pipelineTag,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type hfModelResponse struct {
	Siblings []struct {
		Rfilename string `json:"rfilename"`
		Size      *int64 `json:"size"`
	} `json:"siblings"`
	PipelineTag string   `json:"pipeline_tag"`
	Tags        []string `json:"tags"`
}

type hfTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size *int64 `json:"size"`
	LFS  *struct {
		Size int64 `json:"size"`
	} `json:"lfs"`
}

// hasQuantRe matches a GGUF filename ending with a known quant family prefix
// (longest-first: IQ, TQ, BF, MXFP, NVFP, then single-char Q/F) followed by
// digits. Handles UD-* variant filenames transparently.
var hasQuantRe = regexp.MustCompile(`(?i)[-_](?:UD-)?(?:IQ|TQ|BF|MXFP|NVFP|[QF])\d+\w*\.gguf$`)

// quantDirRe matches a subdirectory name that is itself a quant identifier
// (e.g. "Q8_0", "BF16", "UD-Q5_K_M"). Used to classify subdir-grouped shards.
var quantDirRe = regexp.MustCompile(`(?i)^(?:UD-)?(?:IQ|TQ|BF|MXFP|NVFP|[QF])\d+`)

// quantSuffixRe captures the quant token from the end of a model filename stem
// (after stripping .gguf and any shard suffix). Companion to hasQuantRe/quantDirRe.
// E.g.: "Llama-3-8B-Q4_K_M" → "Q4_K_M", "model-IQ4_XS" → "IQ4_XS".
var quantSuffixRe = regexp.MustCompile(`(?i)[-_]((?:UD-)?(?:IQ|TQ|BF|MXFP|NVFP|[QF])\d+\w*)$`)

func fetchRepoInfo(repoID, token string) (*HFRepoInfo, error) {
	req, err := http.NewRequest("GET", "https://huggingface.co/api/models/"+repoID, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HuggingFace API returned %d", resp.StatusCode)
	}
	var model hfModelResponse
	if err := json.NewDecoder(resp.Body).Decode(&model); err != nil {
		return nil, err
	}

	// Fetch the repo tree for accurate LFS file sizes. GGUF files are LFS
	// objects; the siblings list returns null or the tiny pointer size (~135 B).
	// The tree API returns lfs.size which is the actual file size.
	treeSizes := fetchTreeSizes(repoID, token)

	info := &HFRepoInfo{
		PipelineTag: model.PipelineTag,
		Tags:        model.Tags,
	}

	// subdirAccum groups GGUF shards that live in a subdirectory (e.g.
	// "Q8_0/Model-Q8_0-00001-of-00003.gguf"). The key is the subdir path; the
	// value records the first filename (for glob generation) and total size.
	type subdirAccum struct {
		firstFile string
		total     int64
	}
	subdirs := make(map[string]*subdirAccum)

	for _, s := range model.Siblings {
		size := s.Size
		if sz, ok := treeSizes[s.Rfilename]; ok {
			size = &sz
		}

		// Files in subdirectories are accumulated by dir; handled after the loop.
		if strings.HasSuffix(s.Rfilename, ".gguf") && strings.Contains(s.Rfilename, "/") {
			idx := strings.LastIndex(s.Rfilename, "/")
			dir := s.Rfilename[:idx]
			if subdirs[dir] == nil {
				subdirs[dir] = &subdirAccum{firstFile: s.Rfilename}
			}
			if size != nil {
				subdirs[dir].total += *size
			}
			continue
		}

		f := HFFile{
			Filename:    s.Rfilename,
			Size:        size,
			DownloadURL: "https://huggingface.co/" + repoID + "/resolve/main/" + s.Rfilename,
		}

		if strings.HasSuffix(s.Rfilename, ".gguf") {
			// mmproj files are always companion sidecars even though they carry
			// a precision suffix (f32/f16). Everything else with a quant suffix
			// is a primary model choice; bare names like imatrix.gguf are sidecars.
			if !matchesMmproj(s.Rfilename) && hasQuantRe.MatchString(s.Rfilename) {
				info.Models = append(info.Models, f)
			} else {
				info.Sidecars = append(info.Sidecars, f)
			}
		} else {
			// Non-GGUF files (imatrix.dat, README.md, …) are optional sidecars.
			// Skip git metadata.
			if base := filepath.Base(s.Rfilename); !strings.HasPrefix(base, ".git") {
				info.Sidecars = append(info.Sidecars, f)
			}
		}
	}

	// Emit one HFFile per subdir group. The Filename is the first shard path so
	// shardPattern can derive the correct glob; DisplayName is the dir name
	// (e.g. "Q8_0") which the frontend uses as the tile label.
	for dir, acc := range subdirs {
		dirName := dir
		if idx := strings.LastIndex(dir, "/"); idx >= 0 {
			dirName = dir[idx+1:]
		}
		var sz *int64
		if acc.total > 0 {
			sz = &acc.total
		}
		f := HFFile{
			Filename:    acc.firstFile,
			Size:        sz,
			DisplayName: dirName,
		}
		if quantDirRe.MatchString(dirName) {
			info.Models = append(info.Models, f)
		} else {
			info.Sidecars = append(info.Sidecars, f)
		}
	}

	// Detect vision from companion files.
	for _, s := range info.Sidecars {
		if matchesMmproj(s.Filename) {
			info.IsVision = true
			break
		}
	}
	// Also detect vision from model metadata tags when no mmproj file is present.
	if !info.IsVision {
		if model.PipelineTag == "image-text-to-text" {
			info.IsVision = true
		} else {
			for _, t := range model.Tags {
				switch strings.ToLower(t) {
				case "vision", "multimodal", "image-text-to-text":
					info.IsVision = true
				}
			}
		}
	}
	return info, nil
}

// fetchTreeSizes calls the HF tree API (with pagination) and returns a map of
// filename → actual byte size. For LFS files it uses lfs.size; for regular
// files it uses size. Returns an empty map on any error so callers can fall
// back gracefully.
//
// Uses recursive=true so subdirectory files are included in one shot, then
// follows RFC 5988 Link: <url>; rel="next" headers for any additional pages.
func fetchTreeSizes(repoID, token string) map[string]int64 {
	sizes := make(map[string]int64)
	next := "https://huggingface.co/api/models/" + repoID + "/tree/main?recursive=true"
	for next != "" {
		req, err := http.NewRequest("GET", next, nil)
		if err != nil {
			break
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			break
		}
		var entries []hfTreeEntry
		err = json.NewDecoder(resp.Body).Decode(&entries)
		next = parseLinkNext(resp.Header.Get("Link"))
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			break
		}
		for _, e := range entries {
			if e.Type != "file" {
				continue
			}
			if e.LFS != nil {
				sizes[e.Path] = e.LFS.Size
			} else if e.Size != nil {
				sizes[e.Path] = *e.Size
			}
		}
	}
	return sizes
}

// parseLinkNext extracts the URL for rel="next" from an RFC 5988 Link header.
func parseLinkNext(link string) string {
	for _, seg := range strings.Split(link, ",") {
		seg = strings.TrimSpace(seg)
		if !strings.Contains(seg, `rel="next"`) {
			continue
		}
		s := strings.Index(seg, "<")
		e := strings.Index(seg, ">")
		if s >= 0 && e > s {
			return seg[s+1 : e]
		}
	}
	return ""
}

func matchesMmproj(filename string) bool {
	base := strings.ToLower(filepath.Base(filename))
	return strings.HasPrefix(base, "mmproj-")
}
