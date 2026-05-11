package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func loadProject() (string, Config, State, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", Config{}, State{}, err
	}
	cfg, err := readConfig(root)
	if err != nil {
		return "", Config{}, State{}, fmt.Errorf("not initialized; run ovl init <project-id>: %w", err)
	}
	if cfg.Root != "" && cfg.Root != "." {
		root = filepath.Join(root, cfg.Root)
	}
	var state State
	if err := readJSON(filepath.Join(root, metaDir, "state.json"), &state); err != nil {
		return "", Config{}, State{}, err
	}
	if state.Files == nil {
		state.Files = map[string]FileState{}
	}
	return root, cfg, state, nil
}

func readConfig(root string) (Config, error) {
	var cfg Config
	err := readJSON(filepath.Join(root, metaDir, "config.json"), &cfg)
	return cfg, err
}

func readJSON(path string, dst any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(dst)
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeState(root string, state State, base map[string]FileSnapshot) error {
	if state.Files == nil {
		state.Files = map[string]FileState{}
	}
	if err := writeJSONAtomic(filepath.Join(root, metaDir, "state.json"), state); err != nil {
		return err
	}
	baseRoot := filepath.Join(root, metaDir, "base")
	if err := os.RemoveAll(baseRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(baseRoot, 0o755); err != nil {
		return err
	}
	for p, snap := range base {
		if err := writeProjectFile(baseRoot, p, snap.Content); err != nil {
			return err
		}
	}
	return nil
}

func statesFromSnapshots(snaps map[string]FileSnapshot) map[string]FileState {
	out := map[string]FileState{}
	for p, s := range snaps {
		out[p] = FileState{Type: s.Type, SHA256: s.SHA256, Size: s.Size}
	}
	return out
}

func mergeRemoteIDs(dst, src map[string]FileState) {
	for p, state := range src {
		if state.RemoteID == "" && len(state.RemoteDir) == 0 {
			continue
		}
		current, ok := dst[p]
		if !ok {
			continue
		}
		if state.RemoteID != "" {
			current.RemoteID = state.RemoteID
		}
		if len(state.RemoteDir) > 0 {
			current.RemoteDir = state.RemoteDir
		}
		dst[p] = current
	}
}

func writeProjectFile(root, rel string, content []byte) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}
