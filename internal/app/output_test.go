package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFilterCommentsTreatsResolvedAndUnanchoredIndependently(t *testing.T) {
	comments := []CommentThread{
		{ID: "open-anchored", Anchor: &CommentAnchor{Path: "main.tex"}},
		{ID: "open-unanchored"},
		{ID: "resolved-anchored", Resolved: true, Anchor: &CommentAnchor{Path: "main.tex"}},
		{ID: "resolved-unanchored", Resolved: true, Anchor: &CommentAnchor{DocumentID: "old-doc"}},
	}

	tests := []struct {
		name              string
		includeResolved   bool
		includeUnanchored bool
		want              []string
	}{
		{name: "default", want: []string{"open-anchored"}},
		{name: "resolved", includeResolved: true, want: []string{"open-anchored", "resolved-anchored"}},
		{name: "unanchored", includeUnanchored: true, want: []string{"open-anchored", "open-unanchored"}},
		{name: "all", includeResolved: true, includeUnanchored: true, want: []string{"open-anchored", "open-unanchored", "resolved-anchored", "resolved-unanchored"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := filterComments(comments, test.includeResolved, test.includeUnanchored)
			if len(got) != len(test.want) {
				t.Fatalf("filtered comments = %#v", got)
			}
			for i, id := range test.want {
				if got[i].ID != id {
					t.Fatalf("comments[%d].ID = %q, want %q", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestFilterCommentsByLocationExcludesUnverifiedRangesByDefault(t *testing.T) {
	comments := []CommentThread{
		{ID: "located", Anchor: &CommentAnchor{Path: "main.tex", Line: 4}},
		{ID: "stale-range", Anchor: &CommentAnchor{Path: "main.tex"}},
		{ID: "missing-document"},
	}

	got := filterCommentsByLocation(comments, false)
	if len(got) != 1 || got[0].ID != "located" {
		t.Fatalf("located comments = %#v", got)
	}
	got = filterCommentsByLocation(comments, true)
	if len(got) != len(comments) {
		t.Fatalf("comments including unanchored = %#v", got)
	}
}

func TestWriteCommentsHumanReadable(t *testing.T) {
	timestamp := time.Date(2026, time.October, 15, 17, 27, 38, 0, time.FixedZone("JST", 9*60*60))
	comments := []CommentThread{
		{
			ID:       "thread-without-anchor",
			Resolved: true,
			Messages: []CommentMessage{{
				Content:   "done",
				Timestamp: &timestamp,
				Author:    CommentAuthor{Email: "reviewer@example.com"},
			}},
		},
		{
			ID:     "thread-with-anchor",
			Anchor: &CommentAnchor{Path: "main.tex", OffsetUTF16: 104, Text: "selected", Line: 2, Column: 6, EndLine: 2, EndColumn: 14},
			Messages: []CommentMessage{
				{
					Content:   "Please use your\nfull name.",
					Timestamp: &timestamp,
					Author:    CommentAuthor{Name: "Reviewer", Email: "reviewer@example.com"},
				},
				{
					Content:   "Updated.",
					Timestamp: &timestamp,
					Author:    CommentAuthor{Name: "Author Two"},
				},
			},
		},
	}
	snippets := map[string]commentSnippet{
		"thread-with-anchor": {Lines: []commentSnippetLine{
			{Number: 1, Text: "before"},
			{Number: 2, Text: "text selected here", Selected: true, SelectionStart: 5, SelectionEnd: 13, OpensSelection: true, ClosesSelection: true},
			{Number: 3, Text: "after"},
		}},
	}

	var out bytes.Buffer
	if err := writeComments(&out, comments, snippets, commentOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"main.tex:2:6  [open]",
		"  thread: thread-with-anchor",
		"",
		"    1 | before",
		"  > 2 | text ⟦selected⟧ here",
		"    3 | after",
		"",
		"  Reviewer <reviewer@example.com> — 2026-10-15 08:27 UTC",
		"    │ Please use your",
		"    │ full name.",
		"",
		"  Author Two — 2026-10-15 08:27 UTC",
		"    │ Updated.",
		"",
		"Location unavailable  [resolved]",
		"  thread: thread-without-anchor",
		"",
		"  reviewer@example.com — 2026-10-15 08:27 UTC",
		"    │ done",
		"",
	}, "\n")
	if got := out.String(); got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteCommentsJSONIsIndentedAndSorted(t *testing.T) {
	comments := []CommentThread{
		{ID: "later", Anchor: &CommentAnchor{Path: "z.tex"}, Messages: []CommentMessage{}},
		{ID: "first", Anchor: &CommentAnchor{Path: "a.tex"}, Messages: []CommentMessage{}},
	}

	var out bytes.Buffer
	if err := writeComments(&out, comments, nil, commentOutputOptions{JSON: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "[\n  {") {
		t.Fatalf("JSON was not indented: %q", out.String())
	}
	var got []CommentThread
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "first" || got[1].ID != "later" {
		t.Fatalf("JSON comments = %#v", got)
	}
}

func TestWriteCommentsJSONContract(t *testing.T) {
	timestamp := time.Date(2026, time.October, 15, 8, 27, 38, 0, time.UTC)
	comments := []CommentThread{{
		ID: "thread-id",
		Anchor: &CommentAnchor{
			DocumentID:  "document-id",
			Path:        "main.tex",
			OffsetUTF16: 7,
			Text:        "selected text",
			Line:        3,
			Column:      4,
			EndLine:     3,
			EndColumn:   17,
		},
		Messages: []CommentMessage{{
			ID:        "message-id",
			Content:   "Please clarify.",
			Timestamp: &timestamp,
			Author:    CommentAuthor{ID: "user-id", Name: "Reviewer"},
		}},
	}}

	var out bytes.Buffer
	if err := writeComments(&out, comments, nil, commentOutputOptions{JSON: true, Color: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("JSON output contains ANSI escapes: %q", out.String())
	}
	want := `[
  {
    "id": "thread-id",
    "resolved": false,
    "anchor": {
      "document_id": "document-id",
      "path": "main.tex",
      "offset_utf16": 7,
      "text": "selected text",
      "line": 3,
      "column": 4,
      "end_line": 3,
      "end_column": 17
    },
    "messages": [
      {
        "id": "message-id",
        "content": "Please clarify.",
        "timestamp": "2026-10-15T08:27:38Z",
        "author": {
          "id": "user-id",
          "name": "Reviewer"
        }
      }
    ]
  }
]
`
	if got := out.String(); got != want {
		t.Fatalf("JSON output:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteCommentsJSONEmptyIsArray(t *testing.T) {
	var out bytes.Buffer
	if err := writeComments(&out, []CommentThread{}, nil, commentOutputOptions{JSON: true}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "[]\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestWriteCommentsEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := writeComments(&out, nil, nil, commentOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "No comments.\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestWriteCommentsUnanchoredSeparatesMetadataFromSelectedText(t *testing.T) {
	comments := []CommentThread{{
		ID: "stale-thread",
		Anchor: &CommentAnchor{
			Path:        "main.tex",
			OffsetUTF16: 42,
			Text:        "old selection",
		},
	}}

	var out bytes.Buffer
	if err := writeComments(&out, comments, nil, commentOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"main.tex  [open]",
		"  location unavailable (UTF-16 offset 42)",
		"  thread: stale-thread",
		"",
		"  selected:",
		"    │ old selection",
		"",
	}, "\n")
	if got := out.String(); got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseCommentColorMode(t *testing.T) {
	for _, value := range []string{"auto", "always", "never"} {
		mode, err := parseCommentColorMode(value)
		if err != nil || string(mode) != value {
			t.Fatalf("parseCommentColorMode(%q) = %q, %v", value, mode, err)
		}
	}
	if _, err := parseCommentColorMode("sometimes"); err == nil {
		t.Fatal("invalid color mode was accepted")
	}
}

func TestResolveCommentColor(t *testing.T) {
	tests := []struct {
		name    string
		mode    commentColorMode
		isTTY   bool
		term    string
		noColor string
		goos    string
		want    bool
	}{
		{name: "auto terminal", mode: commentColorAuto, isTTY: true, term: "xterm-256color", goos: "linux", want: true},
		{name: "auto empty no color value", mode: commentColorAuto, isTTY: true, term: "xterm-256color", noColor: "", goos: "linux", want: true},
		{name: "auto pipe", mode: commentColorAuto, term: "xterm-256color", goos: "linux"},
		{name: "auto no color", mode: commentColorAuto, isTTY: true, term: "xterm-256color", noColor: "1", goos: "linux"},
		{name: "auto dumb terminal", mode: commentColorAuto, isTTY: true, term: "dumb", goos: "linux"},
		{name: "auto windows fallback", mode: commentColorAuto, isTTY: true, term: "xterm-256color", goos: "windows"},
		{name: "always overrides environment", mode: commentColorAlways, noColor: "1", goos: "windows", want: true},
		{name: "never", mode: commentColorNever, isTTY: true, term: "xterm-256color", goos: "linux"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveCommentColor(test.mode, test.isTTY, test.term, test.noColor, test.goos); got != test.want {
				t.Fatalf("resolveCommentColor() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestWriteCommentsColorAddsOnlyPresentationEscapes(t *testing.T) {
	comments := []CommentThread{{
		ID:     "thread-id",
		Anchor: &CommentAnchor{Path: "main.tex", Line: 2, Column: 3},
		Messages: []CommentMessage{{
			Author:  CommentAuthor{Name: "Reviewer"},
			Content: "Clarify this.",
		}},
	}}
	snippets := map[string]commentSnippet{
		"thread-id": {Lines: []commentSnippetLine{{
			Number:          2,
			Text:            "a selected phrase",
			Selected:        true,
			SelectionStart:  2,
			SelectionEnd:    10,
			OpensSelection:  true,
			ClosesSelection: true,
		}}},
	}

	var plain, colored bytes.Buffer
	if err := writeComments(&plain, comments, snippets, commentOutputOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := writeComments(&colored, comments, snippets, commentOutputOptions{Color: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(colored.String(), "\x1b[1;36mmain.tex:2:3\x1b[0m") ||
		!strings.Contains(colored.String(), "\x1b[33m[open]\x1b[0m") ||
		!strings.Contains(colored.String(), "\x1b[1;36m⟦selected⟧\x1b[0m") {
		t.Fatalf("expected restrained color styling, got %q", colored.String())
	}
	stripANSI := strings.NewReplacer(
		"\x1b[1;36m", "",
		"\x1b[2;32m", "",
		"\x1b[33m", "",
		"\x1b[2m", "",
		"\x1b[1m", "",
		"\x1b[0m", "",
	)
	if got := stripANSI.Replace(colored.String()); got != plain.String() {
		t.Fatalf("colored output changes content:\n%s\nplain:\n%s", got, plain.String())
	}
}
