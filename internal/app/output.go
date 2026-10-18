package app

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
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

func filterComments(comments []CommentThread, includeResolved bool) []CommentThread {
	filtered := make([]CommentThread, 0, len(comments))
	for _, comment := range comments {
		if !comment.Resolved || includeResolved {
			filtered = append(filtered, comment)
		}
	}
	return filtered
}

func writeComments(w io.Writer, comments []CommentThread, asJSON bool) error {
	comments = sortedComments(comments)
	if asJSON {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(comments)
	}

	var out strings.Builder
	if len(comments) == 0 {
		out.WriteString("No comments.\n")
		_, err := io.WriteString(w, out.String())
		return err
	}
	for i, comment := range comments {
		if i > 0 {
			out.WriteByte('\n')
		}
		status := "open"
		if comment.Resolved {
			status = "resolved"
		}
		if comment.Anchor == nil {
			fmt.Fprintf(&out, "[%s] anchor unavailable (thread %s)\n", status, comment.ID)
		} else {
			path := comment.Anchor.Path
			if path == "" {
				path = "unknown document"
				if comment.Anchor.DocumentID != "" {
					path += " " + comment.Anchor.DocumentID
				}
			}
			fmt.Fprintf(&out, "[%s] %s (offset %d, thread %s)\n", status, path, comment.Anchor.Offset, comment.ID)
			writeIndentedLines(&out, "  selected: ", "            ", comment.Anchor.Text)
		}
		for _, message := range comment.Messages {
			author := message.Author.Name
			if author == "" {
				author = message.Author.Email
			}
			if author == "" {
				author = "Unknown author"
			} else if message.Author.Email != "" && message.Author.Email != author {
				author += " <" + message.Author.Email + ">"
			}
			timestamp := "unknown time"
			if message.Timestamp != nil {
				timestamp = message.Timestamp.UTC().Format("2006-01-02T15:04:05Z07:00")
			}
			fmt.Fprintf(&out, "  %s — %s\n", author, timestamp)
			writeIndentedLines(&out, "    ", "    ", message.Content)
		}
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func sortedComments(comments []CommentThread) []CommentThread {
	sorted := make([]CommentThread, len(comments))
	copy(sorted, comments)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, right := sorted[i], sorted[j]
		if left.Anchor == nil || right.Anchor == nil {
			if left.Anchor == nil && right.Anchor != nil {
				return false
			}
			if left.Anchor != nil && right.Anchor == nil {
				return true
			}
			return left.ID < right.ID
		}
		if (left.Anchor.Path == "") != (right.Anchor.Path == "") {
			return left.Anchor.Path != ""
		}
		if left.Anchor.Path != right.Anchor.Path {
			return left.Anchor.Path < right.Anchor.Path
		}
		if left.Anchor.DocumentID != right.Anchor.DocumentID {
			return left.Anchor.DocumentID < right.Anchor.DocumentID
		}
		if left.Anchor.Offset != right.Anchor.Offset {
			return left.Anchor.Offset < right.Anchor.Offset
		}
		return left.ID < right.ID
	})
	return sorted
}

func writeIndentedLines(out *strings.Builder, firstPrefix, restPrefix, value string) {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if i == 0 {
			out.WriteString(firstPrefix)
		} else {
			out.WriteString(restPrefix)
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
}
