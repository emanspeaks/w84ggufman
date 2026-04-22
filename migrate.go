package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// migrateOldLayout scans modelsDir for old-layout directories and moves them
// to the new modelsDir/org/repo/ structure. After each move it attempts to
// reorganize files within the new repo dir to match the HF layout, and patches
// config file path references.
func migrateOldLayout(cfg Config, pm *presetManager, lsm *llamaSwapManager) {
	dirIgnoreCache := make(map[string][]string)

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
		if isIgnoredAbsolutePath(dirPath, cfg, dirIgnoreCache) {
			continue
		}
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
		patchConfigPaths(pm, lsm, dirPath, targetDir)

		// Reorganize files within the new repo dir to match HF layout.
		existingMeta := readModelMeta(targetDir)
		allRecognized := reorganizeRepoFiles(cfg, targetDir, repoID, cfg.HFToken, pm, lsm)
		updateMetaAfterReorg(targetDir, repoID, existingMeta, allRecognized)
	}
}

// reorganizeExistingLayout handles repos that are already in the org/repo/
// layout but still have a .w84ggufman.json with a repoId (written by a
// previous migration that couldn't complete file reorganization, e.g. due to
// a network failure). It retries reorganization on each startup until all
// files are in their correct HF locations.
func reorganizeExistingLayout(cfg Config, pm *presetManager, lsm *llamaSwapManager) {
	dirIgnoreCache := make(map[string][]string)

	entries, err := os.ReadDir(cfg.ModelsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		orgDir := filepath.Join(cfg.ModelsDir, entry.Name())
		if isIgnoredAbsolutePath(orgDir, cfg, dirIgnoreCache) {
			continue
		}
		if !isOrgDir(orgDir) {
			continue
		}
		repoEntries, _ := os.ReadDir(orgDir)
		for _, repoEntry := range repoEntries {
			if !repoEntry.IsDir() {
				continue
			}
			repoDir := filepath.Join(orgDir, repoEntry.Name())
			if isIgnoredAbsolutePath(repoDir, cfg, dirIgnoreCache) {
				continue
			}
			meta := readModelMeta(repoDir)
			// Only process dirs that have a repoId left from a prior incomplete reorg.
			if meta.RepoID == "" || meta.SkipHFSync {
				continue
			}
			allRecognized := reorganizeRepoFiles(cfg, repoDir, meta.RepoID, cfg.HFToken, pm, lsm)
			updateMetaAfterReorg(repoDir, meta.RepoID, meta, allRecognized)
		}
	}
}

// reorganizeRepoFiles moves files within repoDir to match the HF file layout
// for repoID. Returns true when all files were successfully matched to HF paths
// (no unrecognized files or folders remain after filtering dot files/ignores).
func reorganizeRepoFiles(cfg Config, repoDir, repoID, hfToken string, pm *presetManager, lsm *llamaSwapManager) bool {
	info, err := fetchRepoInfo(repoID, hfToken)
	if err != nil {
		log.Printf("migrate: %s — cannot fetch HF info for reorganization: %v", repoID, err)
		return false
	}
	dirIgnoreCache := make(map[string][]string)

	// Build lookup tables: basename → HF path, and shard-stem → HF representative path.
	allHFFiles := append(info.Models, info.Sidecars...)
	hfByBasename := make(map[string]string)  // base → HF relative path
	hfByStem := make(map[string]string)      // shard stem → HF firstFile path
	hfFullPaths := make(map[string]struct{}) // set of all known HF paths

	for _, f := range allHFFiles {
		base := filepath.Base(f.Filename)
		hfByBasename[base] = f.Filename
		hfFullPaths[f.Filename] = struct{}{}
		// Populate stem map for shard files only (regex modifies the string).
		stemmed := shardRe.ReplaceAllString(base, ".gguf")
		if stemmed != base {
			stem := strings.TrimSuffix(stemmed, ".gguf")
			if _, exists := hfByStem[stem]; !exists {
				hfByStem[stem] = f.Filename
			}
		}
	}

	// Walk repoDir and collect move operations.
	type moveOp struct{ src, dst string }
	var moves []moveOp
	var rogueFiles []string

	filepath.Walk(repoDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.Name() == metaFilename || fi.Name() == metaFilenameYAML || isIgnoredAbsolutePath(path, cfg, dirIgnoreCache) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if fi.IsDir() {
			return nil
		}

		rel, _ := filepath.Rel(repoDir, path)
		rel = filepath.ToSlash(rel)

		// Already at the correct HF path?
		if _, ok := hfFullPaths[rel]; ok {
			return nil
		}

		base := fi.Name()

		// Exact basename match → move to HF path.
		if hfPath, ok := hfByBasename[base]; ok {
			if rel != hfPath {
				moves = append(moves, moveOp{path, filepath.Join(repoDir, filepath.FromSlash(hfPath))})
			}
			return nil
		}

		// Shard stem match → same base name, but in the directory of the HF representative.
		stemmed := shardRe.ReplaceAllString(base, ".gguf")
		if stemmed != base {
			stem := strings.TrimSuffix(stemmed, ".gguf")
			if hfFirst, ok := hfByStem[stem]; ok {
				hfDir := filepath.ToSlash(filepath.Dir(hfFirst))
				var hfPath string
				if hfDir == "." {
					hfPath = base
				} else {
					hfPath = hfDir + "/" + base
				}
				if rel != hfPath {
					moves = append(moves, moveOp{path, filepath.Join(repoDir, filepath.FromSlash(hfPath))})
				}
				return nil
			}
		}

		rogueFiles = append(rogueFiles, rel)
		return nil
	})

	// Execute moves.
	for _, op := range moves {
		destDir := filepath.Dir(op.dst)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			log.Printf("migrate: reorg %s — could not create dir %s: %v", repoID, destDir, err)
			continue
		}
		if err := os.Rename(op.src, op.dst); err != nil {
			log.Printf("migrate: reorg %s — could not move %s: %v", repoID, op.src, err)
			continue
		}
		log.Printf("migrate: reorg %s — %s → %s", repoID, op.src, op.dst)
		// Patch absolute paths in config files for each individual file move.
		patchConfigPaths(pm, lsm, op.src, op.dst)
	}

	cleanEmptyDirs(repoDir)

	if len(moves) > 0 {
		log.Printf("migrate: reorg %s — moved %d file(s), %d unrecognized", repoID, len(moves), len(rogueFiles))
	}
	return len(rogueFiles) == 0
}

// updateMetaAfterReorg writes or deletes .w84ggufman.json after reorganization.
// If allRecognized is true and the meta has no meaningful fields (skip_hf_sync,
// ignore), the file is deleted — the path itself encodes the repoID.
// If unrecognized files remain, the meta is kept as a marker for retry on
// the next startup.
func updateMetaAfterReorg(repoDir, repoID string, existingMeta modelMeta, allRecognized bool) {
	newMeta := modelMeta{
		SkipHFSync: existingMeta.SkipHFSync,
		Ignore:     existingMeta.Ignore,
	}
	if !allRecognized {
		newMeta.RepoID = repoID
	}
	if newMeta.RepoID == "" && !newMeta.SkipHFSync && len(newMeta.Ignore) == 0 {
		p := filepath.Join(repoDir, metaFilename)
		if err := os.Remove(p); err == nil {
			log.Printf("migrate: deleted %s", p)
		}
		return
	}
	_ = writeModelMeta(repoDir, newMeta)
}

// cleanEmptyDirs removes empty subdirectories within root (bottom-up).
func cleanEmptyDirs(root string) {
	var dirs []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		entries, err := os.ReadDir(dirs[i])
		if err == nil && len(entries) == 0 {
			if err := os.Remove(dirs[i]); err == nil {
				log.Printf("migrate: removed empty dir %s", dirs[i])
			}
		}
	}
}

// patchConfigPaths replaces all occurrences of oldPath in models.ini and
// config.yaml with newPath. Used both for directory-level moves (whole repo
// dir relocation) and individual file moves within a repo.
func patchConfigPaths(pm *presetManager, lsm *llamaSwapManager, oldPath, newPath string) {
	oldClean := filepath.Clean(oldPath)
	newClean := filepath.Clean(newPath)

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
