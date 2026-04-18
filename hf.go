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

// quantSuffixRe matches known GGUF quantization and shard suffixes so we can
// strip them to find the base model "stem" for each filename.
var quantSuffixRe = regexp.MustCompile(`(?i)[-_](?:Q[2-9]_[0-9K]+(?:_[MSL])?|IQ[1-4]_\w+|F16|BF16|F32|Q[0-9]+_[0-9]+)$`)
var shardSuffixRe = regexp.MustCompile(`-\d{5}-of-\d{5}$`)

// modelStem strips quantization and shard suffixes from a GGUF filename to
// produce the base model name used for grouping.
func modelStem(filename string) string {
	s := strings.ToLower(strings.TrimSuffix(filepath.Base(filename), ".gguf"))
	for {
		n := quantSuffixRe.ReplaceAllString(s, "")
		n = shardSuffixRe.ReplaceAllString(n, "")
		if n == s {
			break
		}
		s = n
	}
	return s
}

func fetchRepoInfo(repoID, token string) (*HFRepoInfo, error) {
	req, err := http.NewRequest("GET", "https://huggingface.co/api/models/"+repoID, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

	// Group all GGUF files by their model stem. The stem is the filename with
	// quantization and shard suffixes stripped. Files sharing the most common
	// stem are the main model quants; everything else is a companion sidecar.
	type stemGroup struct {
		files []HFFile
	}
	stemMap := map[string]*stemGroup{}
	stemOrder := []string{} // preserve insertion order for stable output
	for _, s := range model.Siblings {
		if !strings.HasSuffix(s.Rfilename, ".gguf") {
			continue
		}
		size := s.Size
		if sz, ok := treeSizes[s.Rfilename]; ok {
			size = &sz
		}
		f := HFFile{
			Filename:    s.Rfilename,
			Size:        size,
			DownloadURL: "https://huggingface.co/" + repoID + "/resolve/main/" + s.Rfilename,
		}
		stem := modelStem(s.Rfilename)
		if _, exists := stemMap[stem]; !exists {
			stemOrder = append(stemOrder, stem)
			stemMap[stem] = &stemGroup{}
		}
		stemMap[stem].files = append(stemMap[stem].files, f)
	}

	// The stem with the most files is the primary model group.
	bestStem := ""
	bestCount := 0
	for _, stem := range stemOrder {
		if n := len(stemMap[stem].files); n > bestCount || (n == bestCount && stem < bestStem) {
			bestCount = n
			bestStem = stem
		}
	}

	info := &HFRepoInfo{
		PipelineTag: model.PipelineTag,
		Tags:        model.Tags,
	}
	for _, stem := range stemOrder {
		g := stemMap[stem]
		if stem == bestStem {
			info.Models = append(info.Models, g.files...)
		} else {
			info.Sidecars = append(info.Sidecars, g.files...)
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

// fetchTreeSizes calls the HF tree API and returns a map of filename → actual
// byte size. For LFS files it uses lfs.size; for regular files it uses size.
// Returns an empty map on any error so callers can fall back gracefully.
func fetchTreeSizes(repoID, token string) map[string]int64 {
	sizes := make(map[string]int64)
	req, err := http.NewRequest("GET", "https://huggingface.co/api/models/"+repoID+"/tree/main", nil)
	if err != nil {
		return sizes
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return sizes
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return sizes
	}
	var entries []hfTreeEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return sizes
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
	return sizes
}

func matchesMmproj(filename string) bool {
	base := strings.ToLower(filepath.Base(filename))
	return strings.HasPrefix(base, "mmproj-")
}
