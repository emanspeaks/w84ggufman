package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type HFFile struct {
	Filename    string `json:"filename"`
	Size        *int64 `json:"size"`
	DownloadURL string `json:"downloadURL"`
}

type HFRepoInfo struct {
	IsVision bool     `json:"isVision"`
	Models   []HFFile `json:"models"`
	Sidecars []HFFile `json:"sidecars"`
}

type hfModelResponse struct {
	Siblings []struct {
		Rfilename string `json:"rfilename"`
		Size      *int64 `json:"size"`
	} `json:"siblings"`
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

	info := &HFRepoInfo{}
	for _, s := range model.Siblings {
		if !strings.HasSuffix(s.Rfilename, ".gguf") {
			continue
		}
		f := HFFile{
			Filename:    s.Rfilename,
			Size:        s.Size,
			DownloadURL: "https://huggingface.co/" + repoID + "/resolve/main/" + s.Rfilename,
		}
		if matchesSidecar(s.Rfilename) {
			info.Sidecars = append(info.Sidecars, f)
		} else {
			info.Models = append(info.Models, f)
		}
	}
	for _, s := range info.Sidecars {
		if matchesMmproj(s.Filename) {
			info.IsVision = true
			break
		}
	}
	return info, nil
}

func matchesSidecar(filename string) bool {
	base := strings.ToLower(filename)
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return strings.HasPrefix(base, "mmproj-")
}

func matchesMmproj(filename string) bool {
	return matchesSidecar(filename)
}
