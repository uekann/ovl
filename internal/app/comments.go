package app

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type commentSnippet struct {
	Lines []commentSnippetLine
}

type commentSnippetLine struct {
	Number          int
	Text            string
	Selected        bool
	SelectionStart  int
	SelectionEnd    int
	OpensSelection  bool
	ClosesSelection bool
}

func filterComments(comments []CommentThread, includeResolved, includeUnanchored bool) []CommentThread {
	filtered := make([]CommentThread, 0, len(comments))
	for _, comment := range comments {
		if comment.Resolved && !includeResolved {
			continue
		}
		if !commentIsAnchored(comment) && !includeUnanchored {
			continue
		}
		filtered = append(filtered, comment)
	}
	return filtered
}

func commentIsAnchored(comment CommentThread) bool {
	return comment.Anchor != nil && comment.Anchor.Path != ""
}

func hasAnchoredComment(comments []CommentThread) bool {
	for _, comment := range comments {
		if commentIsAnchored(comment) {
			return true
		}
	}
	return false
}

// filterCommentsByLocation is applied only after the current remote project
// was fetched successfully. At that point a positive line number means the
// range was verified against the document contents, rather than merely being
// associated with a document path in Overleaf's project tree.
func filterCommentsByLocation(comments []CommentThread, includeUnanchored bool) []CommentThread {
	if includeUnanchored {
		return comments
	}
	filtered := make([]CommentThread, 0, len(comments))
	for _, comment := range comments {
		if comment.Anchor != nil && comment.Anchor.Line > 0 {
			filtered = append(filtered, comment)
		}
	}
	return filtered
}

// enrichCommentLocations translates Overleaf's zero-based UTF-16 offsets into
// one-based line/Unicode-column positions using the current remote document.
// It only accepts an exact text match, so a stale range never points at an
// unrelated part of the file.
func enrichCommentLocations(comments []CommentThread, remote map[string]FileSnapshot) ([]CommentThread, map[string]commentSnippet) {
	enriched := make([]CommentThread, len(comments))
	copy(enriched, comments)
	snippets := make(map[string]commentSnippet)
	for i := range enriched {
		anchor := enriched[i].Anchor
		if anchor == nil || anchor.Path == "" {
			continue
		}
		snapshot, ok := remote[anchor.Path]
		if !ok {
			continue
		}
		located, snippet, ok := locateCommentAnchor(*anchor, snapshot.Content)
		if !ok {
			continue
		}
		enriched[i].Anchor = &located
		snippets[enriched[i].ID] = snippet
	}
	return enriched, snippets
}

func locateCommentAnchor(anchor CommentAnchor, content []byte) (CommentAnchor, commentSnippet, bool) {
	if !utf8.Valid(content) || !utf8.ValidString(anchor.Text) {
		return anchor, commentSnippet{}, false
	}
	source := string(content)
	start, ok := byteIndexAtUTF16Offset(source, anchor.OffsetUTF16)
	if !ok || len(anchor.Text) > len(source)-start || source[start:start+len(anchor.Text)] != anchor.Text {
		return anchor, commentSnippet{}, false
	}
	end := start + len(anchor.Text)
	anchor.Line, anchor.Column = lineColumnAtByte(source, start)
	anchor.EndLine, anchor.EndColumn = lineColumnAtByte(source, end)
	return anchor, makeCommentSnippet(source, anchor), true
}

func byteIndexAtUTF16Offset(value string, offset int) (int, bool) {
	if offset < 0 {
		return 0, false
	}
	units := 0
	for byteIndex, r := range value {
		if units == offset {
			return byteIndex, true
		}
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > offset {
			return 0, false
		}
		units += width
	}
	if units == offset {
		return len(value), true
	}
	return 0, false
}

func lineColumnAtByte(value string, byteIndex int) (int, int) {
	prefix := value[:byteIndex]
	line := strings.Count(prefix, "\n") + 1
	lineStart := strings.LastIndexByte(prefix, '\n') + 1
	column := utf8.RuneCountInString(prefix[lineStart:]) + 1
	return line, column
}

func makeCommentSnippet(source string, anchor CommentAnchor) commentSnippet {
	lines := strings.Split(source, "\n")
	lastSelectedLine := anchor.EndLine
	endColumn := anchor.EndColumn - 1
	if anchor.EndLine > anchor.Line && anchor.EndColumn == 1 {
		lastSelectedLine--
		endColumn = utf8.RuneCountInString(lines[lastSelectedLine-1])
	}
	firstLine := max(1, anchor.Line-1)
	lastLine := min(len(lines), lastSelectedLine+1)
	snippet := commentSnippet{Lines: make([]commentSnippetLine, 0, lastLine-firstLine+1)}
	for lineNumber := firstLine; lineNumber <= lastLine; lineNumber++ {
		text := lines[lineNumber-1]
		selected := lineNumber >= anchor.Line && lineNumber <= lastSelectedLine
		line := commentSnippetLine{Number: lineNumber, Text: text, Selected: selected}
		if selected {
			line.SelectionEnd = utf8.RuneCountInString(text)
			if lineNumber == anchor.Line {
				line.SelectionStart = anchor.Column - 1
				line.OpensSelection = true
			}
			if lineNumber == lastSelectedLine {
				line.SelectionEnd = endColumn
				line.ClosesSelection = true
			}
		}
		snippet.Lines = append(snippet.Lines, line)
	}
	return snippet
}

func writeComments(w io.Writer, comments []CommentThread, snippets map[string]commentSnippet, asJSON bool) error {
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
		if !commentIsAnchored(comment) {
			fmt.Fprintf(&out, "[%s] anchor unavailable (thread %s)\n", status, terminalSafe(comment.ID))
			if comment.Anchor != nil && comment.Anchor.Text != "" {
				writeIndentedLines(&out, "  selected: ", "            ", comment.Anchor.Text)
			}
		} else if comment.Anchor.Line > 0 {
			fmt.Fprintf(&out, "[%s] %s:%d:%d (thread %s)\n", status, terminalSafe(comment.Anchor.Path), comment.Anchor.Line, comment.Anchor.Column, terminalSafe(comment.ID))
			if snippet, ok := snippets[comment.ID]; ok {
				writeCommentSnippet(&out, snippet)
			} else {
				writeIndentedLines(&out, "  selected: ", "            ", comment.Anchor.Text)
			}
		} else {
			fmt.Fprintf(&out, "[%s] %s (UTF-16 offset %d, thread %s)\n", status, terminalSafe(comment.Anchor.Path), comment.Anchor.OffsetUTF16, terminalSafe(comment.ID))
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
			fmt.Fprintf(&out, "  %s — %s\n", terminalSafe(author), timestamp)
			writeIndentedLines(&out, "    ", "    ", message.Content)
		}
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func writeCommentSnippet(out io.Writer, snippet commentSnippet) {
	if len(snippet.Lines) == 0 {
		return
	}
	width := len(strconv.Itoa(snippet.Lines[len(snippet.Lines)-1].Number))
	for _, line := range snippet.Lines {
		marker := " "
		if line.Selected {
			marker = ">"
		}
		fmt.Fprintf(out, "  %s %*d | %s\n", marker, width, line.Number, markedSnippetText(line))
	}
}

func markedSnippetText(line commentSnippetLine) string {
	runes := []rune(line.Text)
	start := min(max(0, line.SelectionStart), len(runes))
	end := min(max(start, line.SelectionEnd), len(runes))
	var out strings.Builder
	for i := 0; i <= len(runes); i++ {
		if line.OpensSelection && i == start {
			out.WriteString("⟦")
		}
		if line.ClosesSelection && i == end {
			out.WriteString("⟧")
		}
		if i < len(runes) {
			out.WriteRune(runes[i])
		}
	}
	return terminalSafe(out.String())
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
		if left.Anchor.OffsetUTF16 != right.Anchor.OffsetUTF16 {
			return left.Anchor.OffsetUTF16 < right.Anchor.OffsetUTF16
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
		out.WriteString(terminalSafe(line))
		out.WriteByte('\n')
	}
}

func terminalSafe(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r == '\t' {
			out.WriteString(`\t`)
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			fmt.Fprintf(&out, `\u%04X`, r)
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
