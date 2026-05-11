package app

import (
	"path/filepath"
	"time"
)

func backupRemote(root string, remote map[string]FileSnapshot) (string, error) {
	name := "remote-" + time.Now().Format("2006-01-02T15-04-05")
	dir := filepath.Join(root, metaDir, "backups", name)
	for p, snap := range remote {
		if err := writeProjectFile(dir, p, snap.Content); err != nil {
			return "", err
		}
	}
	if err := writeJSONAtomic(filepath.Join(dir, "metadata.json"), statesFromSnapshots(remote)); err != nil {
		return "", err
	}
	return dir, nil
}

func backupLocal(root string, local map[string]FileSnapshot) (string, error) {
	name := "local-" + time.Now().Format("2006-01-02T15-04-05")
	dir := filepath.Join(root, metaDir, "backups", name)
	for p, snap := range local {
		if err := writeProjectFile(dir, p, snap.Content); err != nil {
			return "", err
		}
	}
	if err := writeJSONAtomic(filepath.Join(dir, "metadata.json"), statesFromSnapshots(local)); err != nil {
		return "", err
	}
	return dir, nil
}
