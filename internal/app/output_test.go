package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFilterCommentsExcludesResolvedByDefault(t *testing.T) {
	comments := []CommentThread{
		{ID: "open"},
		{ID: "resolved", Resolved: true},
	}

	got := filterComments(comments, false)
	if len(got) != 1 || got[0].ID != "open" {
		t.Fatalf("filtered comments = %#v", got)
	}
	got = filterComments(comments, true)
	if len(got) != 2 {
		t.Fatalf("comments including resolved = %#v", got)
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
			Anchor: &CommentAnchor{Path: "main.tex", Offset: 104, Text: "first\nsecond"},
			Messages: []CommentMessage{{
				Content:   "Please use your\nfull name.",
				Timestamp: &timestamp,
				Author:    CommentAuthor{Name: "Reviewer", Email: "reviewer@example.com"},
			}},
		},
	}

	var out bytes.Buffer
	if err := writeComments(&out, comments, false); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"[open] main.tex (offset 104, thread thread-with-anchor)",
		"  selected: first",
		"            second",
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
	if err := writeComments(&out, comments, true); err != nil {
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
			DocumentID: "document-id",
			Path:       "main.tex",
			Offset:     7,
			Text:       "selected text",
		},
		Messages: []CommentMessage{{
			ID:        "message-id",
			Content:   "Please clarify.",
			Timestamp: &timestamp,
			Author:    CommentAuthor{ID: "user-id", Name: "Reviewer"},
		}},
	}}

	var out bytes.Buffer
	if err := writeComments(&out, comments, true); err != nil {
		t.Fatal(err)
	}
	want := `[
  {
    "id": "thread-id",
    "resolved": false,
    "anchor": {
      "document_id": "document-id",
      "path": "main.tex",
      "offset": 7,
      "text": "selected text"
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
	if err := writeComments(&out, []CommentThread{}, true); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "[]\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestWriteCommentsEmpty(t *testing.T) {
	var out bytes.Buffer
	if err := writeComments(&out, nil, false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "No comments.\n" {
		t.Fatalf("output = %q", got)
	}
}
