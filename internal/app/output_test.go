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
			Messages: []CommentMessage{{
				Content:   "Please use your\nfull name.",
				Timestamp: &timestamp,
				Author:    CommentAuthor{Name: "Reviewer", Email: "reviewer@example.com"},
			}},
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
	if err := writeComments(&out, comments, snippets, false); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"[open] main.tex:2:6 (thread thread-with-anchor)",
		"    1 | before",
		"  > 2 | text ⟦selected⟧ here",
		"    3 | after",
		"  Reviewer <reviewer@example.com> — 2026-10-15T08:27:38Z",
		"    Please use your",
		"    full name.",
		"",
		"[resolved] anchor unavailable (thread thread-without-anchor)",
		"  reviewer@example.com — 2026-10-15T08:27:38Z",
		"    done",
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
	if err := writeComments(&out, comments, nil, true); err != nil {
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
	if err := writeComments(&out, comments, nil, true); err != nil {
		t.Fatal(err)
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
	if err := writeComments(&out, []CommentThread{}, nil, true); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "[]\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestWriteCommentsEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := writeComments(&out, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "No comments.\n" {
		t.Fatalf("output = %q", got)
	}
}
