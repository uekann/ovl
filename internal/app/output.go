package app

import (
	"fmt"
	"sort"
)

func printLocalStatus(base map[string]FileState, local map[string]FileSnapshot) {
	var changed []string
	for _, p := range allKeys(base, local, nil) {
		b, hasBase := base[p]
		l, hasLocal := local[p]
		if changedFromBase(hasBase, b.SHA256, hasLocal, l.SHA256) {
			changed = append(changed, p)
		}
	}
	printPaths("Local changes:", changed)
	if len(changed) == 0 {
		fmt.Println("Clean")
	}
}

func printPaths(title string, paths []string) {
	if len(paths) == 0 {
		return
	}
	fmt.Println(title)
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Printf("  - %s\n", p)
	}
}

func printRemoteDeleted(base map[string]FileState, local, remote map[string]FileSnapshot) {
	var deleted []string
	for p := range base {
		if _, ok := local[p]; ok {
			if _, ok := remote[p]; !ok {
				deleted = append(deleted, p)
			}
		}
	}
	if len(deleted) == 0 {
		return
	}
	printPaths("Remote file was deleted. Not deleting local file by default:", deleted)
	fmt.Println("\nUse:\n  ovl pull --delete-local")
}

func printLocalDeleted(base map[string]FileState, local, remote map[string]FileSnapshot) {
	var deleted []string
	for p := range base {
		if _, ok := local[p]; !ok {
			if _, ok := remote[p]; ok {
				deleted = append(deleted, p)
			}
		}
	}
	if len(deleted) == 0 {
		return
	}
	printPaths("Local file was deleted. Not deleting remote file by default:", deleted)
	fmt.Println("\nUse:\n  ovl push --delete")
}
