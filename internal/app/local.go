package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func scanLocal(root string, cfg Config) (map[string]FileSnapshot, error) {
	ignores, err := readIgnores(root, cfg.IgnoreFile)
	if err != nil {
		return nil, err
	}
	out := map[string]FileSnapshot{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if ignored(rel, d.IsDir(), ignores) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		snap, err := snapshotFile(root, rel)
		if err != nil {
			return err
		}
		out[rel] = snap
		return nil
	})
	return out, err
}

func snapshotFile(root, rel string) (FileSnapshot, error) {
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return FileSnapshot{}, err
	}
	sum := sha256.Sum256(content)
	return FileSnapshot{
		Path:    rel,
		Type:    fileType(content),
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(content)),
		Content: content,
	}, nil
}

func fileType(content []byte) string {
	if strings.IndexByte(string(content), 0) >= 0 {
		return "binary"
	}
	return "text"
}

func readIgnores(root, ignoreFile string) ([]string, error) {
	patterns := []string{".git/", ".gitignore", metaDir + "/"}
	if ignoreFile == "" {
		return patterns, nil
	}
	patterns = append(patterns, ignoreFile)
	b, err := os.ReadFile(filepath.Join(root, ignoreFile))
	if errors.Is(err, os.ErrNotExist) {
		return patterns, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, filepath.ToSlash(line))
	}
	return patterns, nil
}

func ignored(path string, isDir bool, patterns []string) bool {
	for _, p := range patterns {
		if strings.HasSuffix(p, "/") {
			prefix := strings.TrimSuffix(p, "/")
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				return true
			}
			continue
		}
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
		if ok, _ := filepath.Match(p, filepath.Base(path)); ok {
			return true
		}
		if isDir && path == strings.TrimSuffix(p, "/") {
			return true
		}
	}
	return false
}
