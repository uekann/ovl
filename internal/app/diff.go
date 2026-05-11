package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

func printDiff(root string, target map[string]FileSnapshot) {
	baseRoot := filepath.Join(root, metaDir, "base")
	keys := allSnapshotKeys(target, baseRoot)
	for _, p := range keys {
		baseContent, baseErr := os.ReadFile(filepath.Join(baseRoot, filepath.FromSlash(p)))
		snap, hasTarget := target[p]
		if baseErr == nil && hasTarget && string(baseContent) == string(snap.Content) {
			continue
		}
		if hasTarget && snap.Type == "binary" {
			fmt.Printf("%s: binary changed size=%d sha256=%s\n", p, snap.Size, snap.SHA256)
			continue
		}
		if baseErr == nil && isBinary(baseContent) {
			fmt.Printf("%s: binary base changed\n", p)
			continue
		}
		var targetContent []byte
		if hasTarget {
			targetContent = snap.Content
		}
		fmt.Print(unifiedDiff(p, baseContent, targetContent, baseErr == nil, hasTarget))
	}
}

func allSnapshotKeys(target map[string]FileSnapshot, baseRoot string) []string {
	set := map[string]bool{}
	for p := range target {
		set[p] = true
	}
	_ = filepath.WalkDir(baseRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(baseRoot, path)
		if err == nil {
			set[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	var out []string
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func unifiedDiff(path string, old, new []byte, hasOld, hasNew bool) string {
	if !hasOld {
		old = nil
	}
	if !hasNew {
		new = nil
	}
	if string(old) == string(new) {
		return ""
	}
	lines := diffLines(string(old), string(new))
	hunks := diffHunks(lines, 3)
	if len(hunks) == 0 {
		return ""
	}
	var b strings.Builder
	oldName := "a/" + path
	newName := "b/" + path
	if !hasOld {
		oldName = "/dev/null"
	}
	if !hasNew {
		newName = "/dev/null"
	}
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldName, newName)
	for _, hunk := range hunks {
		oldStart, oldCount, newStart, newCount := hunkRange(hunk)
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for _, line := range hunk {
			fmt.Fprintf(&b, "%c%s\n", line.op, line.text)
		}
	}
	return b.String()
}

type diffLine struct {
	op    byte
	text  string
	oldNo int
	newNo int
}

func diffLines(oldText, newText string) []diffLine {
	dmp := diffmatchpatch.New()
	oldChars, newChars, lineArray := dmp.DiffLinesToChars(oldText, newText)
	diffs := dmp.DiffMain(oldChars, newChars, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)
	dmp.DiffCleanupSemantic(diffs)

	var out []diffLine
	oldNo, newNo := 1, 1
	for _, diff := range diffs {
		for _, line := range splitDiffText(diff.Text) {
			switch diff.Type {
			case diffmatchpatch.DiffEqual:
				out = append(out, diffLine{op: ' ', text: line, oldNo: oldNo, newNo: newNo})
				oldNo++
				newNo++
			case diffmatchpatch.DiffDelete:
				out = append(out, diffLine{op: '-', text: line, oldNo: oldNo, newNo: newNo})
				oldNo++
			case diffmatchpatch.DiffInsert:
				out = append(out, diffLine{op: '+', text: line, oldNo: oldNo, newNo: newNo})
				newNo++
			}
		}
	}
	return out
}

func splitDiffText(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func diffHunks(lines []diffLine, contextLines int) [][]diffLine {
	var hunks [][]diffLine
	var current []diffLine
	var preContext []diffLine
	trailingEqual := 0
	for _, line := range lines {
		if line.op == ' ' {
			if current == nil {
				preContext = append(preContext, line)
				if len(preContext) > contextLines {
					preContext = preContext[len(preContext)-contextLines:]
				}
				continue
			}
			current = append(current, line)
			trailingEqual++
			if trailingEqual > contextLines {
				flushLen := len(current) - contextLines
				hunks = append(hunks, append([]diffLine(nil), current[:flushLen]...))
				preContext = append([]diffLine(nil), current[flushLen:]...)
				current = nil
				trailingEqual = 0
			}
			continue
		}
		if current == nil {
			current = append([]diffLine(nil), preContext...)
		}
		current = append(current, line)
		trailingEqual = 0
	}
	if current != nil {
		hunks = append(hunks, current)
	}
	return hunks
}

func hunkRange(lines []diffLine) (int, int, int, int) {
	oldStart, newStart := 0, 0
	oldCount, newCount := 0, 0
	for _, line := range lines {
		if line.op != '+' {
			if oldStart == 0 {
				oldStart = line.oldNo
			}
			oldCount++
		}
		if line.op != '-' {
			if newStart == 0 {
				newStart = line.newNo
			}
			newCount++
		}
	}
	first := lines[0]
	if oldStart == 0 {
		oldStart = first.oldNo - 1
		if oldStart < 0 {
			oldStart = 0
		}
	}
	if newStart == 0 {
		newStart = first.newNo - 1
		if newStart < 0 {
			newStart = 0
		}
	}
	return oldStart, oldCount, newStart, newCount
}

func isBinary(content []byte) bool {
	return strings.IndexByte(string(content), 0) >= 0
}
