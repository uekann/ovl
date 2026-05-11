package app

import (
	"sort"
	"strings"
)

type changeReport struct {
	Local     []string
	Remote    []string
	Conflicts []string
}

func compare(base map[string]FileState, local, remote map[string]FileSnapshot) changeReport {
	keys := allKeys(base, local, remote)
	var r changeReport
	for _, p := range keys {
		b, hasBase := base[p]
		l, hasLocal := local[p]
		m, hasRemote := remote[p]
		localChanged := changedFromBase(hasBase, b.SHA256, hasLocal, l.SHA256)
		remoteChanged := changedFromBase(hasBase, b.SHA256, hasRemote, m.SHA256)
		if localChanged && remoteChanged {
			if hasLocal && hasRemote && l.SHA256 == m.SHA256 {
				continue
			}
			r.Conflicts = append(r.Conflicts, p)
			continue
		}
		if localChanged {
			r.Local = append(r.Local, p)
		}
		if remoteChanged {
			r.Remote = append(r.Remote, p)
		}
	}
	return r
}

func changedFromBase(hasBase bool, baseHash string, exists bool, hash string) bool {
	if !hasBase {
		return exists
	}
	if !exists {
		return true
	}
	return baseHash != hash
}

func pullWrites(base map[string]FileState, local, remote map[string]FileSnapshot, force bool) []string {
	var out []string
	for _, p := range allKeys(base, local, remote) {
		b, hasBase := base[p]
		l, hasLocal := local[p]
		m, hasRemote := remote[p]
		if !hasRemote {
			continue
		}
		if force {
			if !hasLocal || l.SHA256 != m.SHA256 {
				out = append(out, p)
			}
			continue
		}
		localChanged := changedFromBase(hasBase, b.SHA256, hasLocal, l.SHA256)
		remoteChanged := changedFromBase(hasBase, b.SHA256, hasRemote, m.SHA256)
		if remoteChanged && !localChanged {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func pullDeletes(base map[string]FileState, local, remote map[string]FileSnapshot, deleteLocal, force bool) []string {
	if !deleteLocal && !force {
		return nil
	}
	var out []string
	for p, l := range local {
		_, hasRemote := remote[p]
		b, hasBase := base[p]
		if hasRemote {
			continue
		}
		if force || (hasBase && l.SHA256 == b.SHA256) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func pushUploads(base map[string]FileState, local, remote map[string]FileSnapshot, force bool) []string {
	var out []string
	for _, p := range allKeys(base, local, remote) {
		l, hasLocal := local[p]
		if !hasLocal {
			continue
		}
		b, hasBase := base[p]
		m, hasRemote := remote[p]
		localChanged := changedFromBase(hasBase, b.SHA256, true, l.SHA256)
		remoteChanged := changedFromBase(hasBase, b.SHA256, hasRemote, m.SHA256)
		if localChanged && (!remoteChanged || force) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func pushDeletes(base map[string]FileState, local, remote map[string]FileSnapshot, enabled, force bool) []string {
	if !enabled && !force {
		return nil
	}
	var out []string
	if force {
		for p := range remote {
			if _, hasLocal := local[p]; !hasLocal {
				out = append(out, p)
			}
		}
		sort.Strings(out)
		return out
	}
	for p := range base {
		if _, hasLocal := local[p]; hasLocal {
			continue
		}
		if _, hasRemote := remote[p]; hasRemote {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func pushDeletedDirs(local, remote map[string]FileSnapshot, deletes []string) []string {
	if len(deletes) == 0 {
		return nil
	}
	deleteSet := map[string]bool{}
	candidates := map[string]bool{}
	for _, p := range deletes {
		deleteSet[p] = true
		for dir := parentDir(p); dir != ""; dir = parentDir(dir) {
			candidates[dir] = true
		}
	}
	var out []string
	for dir := range candidates {
		prefix := dir + "/"
		hasLocalFile := false
		for p := range local {
			if strings.HasPrefix(p, prefix) {
				hasLocalFile = true
				break
			}
		}
		if hasLocalFile {
			continue
		}
		hasRemoteFile := false
		hasRemainingRemoteFile := false
		for p := range remote {
			if !strings.HasPrefix(p, prefix) {
				continue
			}
			hasRemoteFile = true
			if !deleteSet[p] {
				hasRemainingRemoteFile = true
				break
			}
		}
		if hasRemoteFile && !hasRemainingRemoteFile {
			out = append(out, dir)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if strings.Count(out[i], "/") != strings.Count(out[j], "/") {
			return strings.Count(out[i], "/") > strings.Count(out[j], "/")
		}
		return out[i] < out[j]
	})
	return out
}

func parentDir(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx]
}

func dirStateForDelete(dir string, files map[string]FileState) FileState {
	var out FileState
	prefix := dir + "/"
	for path, state := range files {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		if id := state.RemoteDir[dir]; id != "" {
			out.RemoteID = id
			return out
		}
	}
	return out
}
