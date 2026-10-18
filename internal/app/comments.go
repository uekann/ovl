package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
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

type commentOutputOptions struct {
	JSON  bool
	Color bool
}

type commentColorMode string

const (
	commentColorAuto   commentColorMode = "auto"
	commentColorAlways commentColorMode = "always"
	commentColorNever  commentColorMode = "never"
)

type commentStyle struct {
	color bool
}

func parseCommentColorMode(value string) (commentColorMode, error) {
	mode := commentColorMode(value)
	switch mode {
	case commentColorAuto, commentColorAlways, commentColorNever:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid --color value %q: want auto, always, or never", value)
	}
}

func commentColorEnabled(mode commentColorMode, w io.Writer) bool {
	isTTY := false
	if file, ok := w.(*os.File); ok {
		isTTY = term.IsTerminal(int(file.Fd()))
	}
	return resolveCommentColor(mode, isTTY, os.Getenv("TERM"), os.Getenv("NO_COLOR"), runtime.GOOS)
}

func resolveCommentColor(mode commentColorMode, isTTY bool, termEnv, noColor, goos string) bool {
	switch mode {
	case commentColorAlways:
		return true
	case commentColorNever:
		return false
	case commentColorAuto:
		// Older Windows consoles may report a terminal without supporting ANSI
		// escape sequences. Users can still opt in with --color=always.
		return isTTY && termEnv != "dumb" && noColor == "" && goos != "windows"
	default:
		return false
	}
}

func (s commentStyle) apply(code, value string) string {
	if !s.color || value == "" {
		return value
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

func (s commentStyle) location(value string) string { return s.apply("1;36", value) }
func (s commentStyle) open(value string) string     { return s.apply("33", value) }
func (s commentStyle) resolved(value string) string { return s.apply("2;32", value) }
func (s commentStyle) metadata(value string) string { return s.apply("2", value) }
func (s commentStyle) author(value string) string   { return s.apply("1", value) }
func (s commentStyle) selected(value string) string { return s.apply("1;36", value) }

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

func writeComments(w io.Writer, comments []CommentThread, snippets map[string]commentSnippet, options commentOutputOptions) error {
	comments = sortedComments(comments)
	if options.JSON {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(comments)
	}

	style := commentStyle{color: options.Color}
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
		location := "Location unavailable"
		if comment.Anchor != nil && comment.Anchor.Path != "" {
			location = terminalSafe(comment.Anchor.Path)
			if comment.Anchor.Line > 0 {
				location += fmt.Sprintf(":%d:%d", comment.Anchor.Line, comment.Anchor.Column)
			}
		}
		fmt.Fprintf(&out, "%s  %s\n", style.location(location), styledCommentStatus(style, comment.Resolved))
		if comment.Anchor != nil && comment.Anchor.Path != "" && comment.Anchor.Line == 0 {
			metadata := fmt.Sprintf("location unavailable (UTF-16 offset %d)", comment.Anchor.OffsetUTF16)
			fmt.Fprintf(&out, "  %s\n", style.metadata(metadata))
		}
		fmt.Fprintf(&out, "  %s\n", style.metadata("thread: "+terminalSafe(comment.ID)))

		if comment.Anchor != nil && comment.Anchor.Line > 0 {
			if snippet, ok := snippets[comment.ID]; ok && len(snippet.Lines) > 0 {
				out.WriteByte('\n')
				writeCommentSnippetStyled(&out, snippet, style)
			} else if comment.Anchor.Text != "" {
				out.WriteByte('\n')
				writeCommentTextBlock(&out, "selected:", comment.Anchor.Text, style, true)
			}
		} else if comment.Anchor != nil && comment.Anchor.Text != "" {
			out.WriteByte('\n')
			writeCommentTextBlock(&out, "selected:", comment.Anchor.Text, style, true)
		}

		if len(comment.Messages) > 0 {
			out.WriteByte('\n')
		}
		for messageIndex, message := range comment.Messages {
			if messageIndex > 0 {
				out.WriteByte('\n')
			}
			timestamp := "unknown time"
			if message.Timestamp != nil {
				timestamp = message.Timestamp.UTC().Format("2006-01-02 15:04 UTC")
			}
			fmt.Fprintf(&out, "  %s %s %s\n",
				styledCommentAuthor(style, message.Author),
				style.metadata("—"),
				style.metadata(timestamp),
			)
			writeCommentTextBlock(&out, "", message.Content, style, false)
		}
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func writeCommentSnippet(out io.Writer, snippet commentSnippet) {
	writeCommentSnippetStyled(out, snippet, commentStyle{})
}

func writeCommentSnippetStyled(out io.Writer, snippet commentSnippet, style commentStyle) {
	if len(snippet.Lines) == 0 {
		return
	}
	width := len(strconv.Itoa(snippet.Lines[len(snippet.Lines)-1].Number))
	for _, line := range snippet.Lines {
		marker := " "
		if line.Selected {
			marker = style.selected(">")
		}
		lineNumber := style.metadata(fmt.Sprintf("%*d", width, line.Number))
		fmt.Fprintf(out, "  %s %s %s %s\n", marker, lineNumber, style.metadata("|"), markedSnippetTextStyled(line, style))
	}
}

func markedSnippetText(line commentSnippetLine) string {
	return markedSnippetTextStyled(line, commentStyle{})
}

func markedSnippetTextStyled(line commentSnippetLine, style commentStyle) string {
	runes := []rune(line.Text)
	if !line.Selected {
		return terminalSafe(line.Text)
	}
	start := min(max(0, line.SelectionStart), len(runes))
	end := min(max(start, line.SelectionEnd), len(runes))
	selected := terminalSafe(string(runes[start:end]))
	if line.OpensSelection {
		selected = "⟦" + selected
	}
	if line.ClosesSelection {
		selected += "⟧"
	}
	return terminalSafe(string(runes[:start])) + style.selected(selected) + terminalSafe(string(runes[end:]))
}

func styledCommentStatus(style commentStyle, resolved bool) string {
	if resolved {
		return style.resolved("[resolved]")
	}
	return style.open("[open]")
}

func styledCommentAuthor(style commentStyle, author CommentAuthor) string {
	name := terminalSafe(author.Name)
	email := terminalSafe(author.Email)
	if name == "" && email == "" {
		return style.author("Unknown author")
	}
	if name == "" {
		return style.author(email)
	}
	formatted := style.author(name)
	if email != "" && email != name {
		formatted += " " + style.metadata("<"+email+">")
	}
	return formatted
}

func writeCommentTextBlock(out *strings.Builder, label, value string, style commentStyle, selected bool) {
	if label != "" {
		fmt.Fprintf(out, "  %s\n", style.metadata(label))
	}
	for _, line := range strings.Split(value, "\n") {
		text := terminalSafe(line)
		if selected {
			text = style.selected(text)
		}
		fmt.Fprintf(out, "    %s %s\n", style.metadata("│"), text)
	}
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
