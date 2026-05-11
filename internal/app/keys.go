package app

import "sort"

func allKeys(base map[string]FileState, local, remote map[string]FileSnapshot) []string {
	set := map[string]bool{}
	for p := range base {
		set[p] = true
	}
	for p := range local {
		set[p] = true
	}
	for p := range remote {
		set[p] = true
	}
	var out []string
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
