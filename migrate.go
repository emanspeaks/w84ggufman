package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// migrateOldLayout scans modelsDir for old-layout directories and moves them
// to the new modelsDir/org/repo/ structure, patching config file path
// references as it goes. Dirs with skip_hf_sync=true are left in place.
func migrateOldLayout(cfg Config, pm *presetManager, lsm *llamaSwapManager) {
	entries, err := os.ReadDir(cfg.ModelsDir)
	if err != nil {
		log.Printf("migrate: could not read models dir: %v", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(cfg.ModelsDir, entry.Name())
		if isOrgDir(dirPath) {
			// Already new org/repo layout — skip.
			continue
		}

		meta := readModelMeta(dirPath)
		if meta.SkipHFSync {
			continue // explicitly local, leave in place
		}

		repoID := meta.RepoID
		if repoID == "" {
			// Try GGUF metadata detection.
			files, _ := scanFilesRelative(dirPath)
			var ggufFiles []string
			for _, f := range files {
				b := filepath.Base(f)
				if strings.HasSuffix(b, ".gguf") && !matchesMmproj(b) {
					ggufFiles = append(ggufFiles, f)
				}
			}
			if len(ggufFiles) > 0 {
				repoID = detectRepoIDFromGGUF(dirPath, ggufFiles)
			}
		}

		if repoID == "" {
			log.Printf("migrate: %s — could not determine HF source, skipping", entry.Name())
			continue
		}

		parts := strings.SplitN(repoID, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			log.Printf("migrate: %s — invalid repoID %q, skipping", entry.Name(), repoID)
			continue
		}
		org, repo := parts[0], parts[1]

		orgDir := filepath.Join(cfg.ModelsDir, org)
		targetDir := filepath.Join(orgDir, repo)

		if filepath.Clean(dirPath) == filepath.Clean(targetDir) {
			continue // already in place
		}

		if _, err := os.Stat(targetDir); err == nil {
			log.Printf("migrate: %s — target %s already exists, skipping", entry.Name(), targetDir)
			continue
		}

		if err := os.MkdirAll(orgDir, 0755); err != nil {
			log.Printf("migrate: %s — could not create org dir: %v", entry.Name(), err)
			continue
		}

		if err := os.Rename(dirPath, targetDir); err != nil {
			log.Printf("migrate: %s — rename failed: %v", entry.Name(), err)
			continue
		}

		log.Printf("migrate: moved %s → %s", dirPath, targetDir)

		_ = writeModelMeta(targetDir, repoID)
		patchConfigPaths(pm, lsm, dirPath, targetDir)
	}
}

// patchConfigPaths replaces all occurrences of oldDir in models.ini and
// config.yaml with newDir, repairing path references after migration.
func patchConfigPaths(pm *presetManager, lsm *llamaSwapManager, oldDir, newDir string) {
	oldClean := filepath.Clean(oldDir)
	newClean := filepath.Clean(newDir)

	patch := func(readAll func() (string, error), writeAll func(string) error, name string) {
		body, err := readAll()
		if err != nil || !strings.Contains(body, oldClean) {
			return
		}
		patched := strings.ReplaceAll(body, oldClean, newClean)
		if err := writeAll(patched); err != nil {
			log.Printf("migrate: could not patch %s: %v", name, err)
		} else {
			log.Printf("migrate: patched paths in %s", name)
		}
	}

	patch(pm.ReadAll, pm.WriteAll, "models.ini")
	if lsm != nil {
		patch(lsm.ReadAll, lsm.WriteAll, "config.yaml")
	}
}
