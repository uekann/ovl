package app

import (
	"bytes"
	"testing"
)

func TestEnrichCommentLocationsUsesUTF16OffsetAndBuildsContext(t *testing.T) {
	comments := []CommentThread{{
		ID: "thread-id",
		Anchor: &CommentAnchor{
			Path:        "main.tex",
			OffsetUTF16: 11,
			Text:        "selected",
		},
	}}
	remote := map[string]FileSnapshot{
		"main.tex": {Content: []byte("intro\n😀 x selected tail\noutro\n")},
	}

	got, snippets := enrichCommentLocations(comments, remote)
	anchor := got[0].Anchor
	if anchor.Line != 2 || anchor.Column != 5 || anchor.EndLine != 2 || anchor.EndColumn != 13 {
		t.Fatalf("anchor location = %#v", anchor)
	}
	if comments[0].Anchor.Line != 0 {
		t.Fatal("input comment was mutated")
	}
	var out bytes.Buffer
	writeCommentSnippet(&out, snippets["thread-id"])
	want := "    1 | intro\n  > 2 | 😀 x ⟦selected⟧ tail\n    3 | outro\n"
	if out.String() != want {
		t.Fatalf("snippet:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestEnrichCommentLocationsMarksMultilineSelection(t *testing.T) {
	comments := []CommentThread{{
		ID: "thread-id",
		Anchor: &CommentAnchor{
			Path:        "main.tex",
			OffsetUTF16: 12,
			Text:        "beta\ngamma",
		},
	}}
	remote := map[string]FileSnapshot{
		"main.tex": {Content: []byte("first\nalpha beta\ngamma delta\nlast")},
	}

	got, snippets := enrichCommentLocations(comments, remote)
	anchor := got[0].Anchor
	if anchor.Line != 2 || anchor.Column != 7 || anchor.EndLine != 3 || anchor.EndColumn != 6 {
		t.Fatalf("anchor location = %#v", anchor)
	}
	var out bytes.Buffer
	writeCommentSnippet(&out, snippets["thread-id"])
	want := "    1 | first\n  > 2 | alpha ⟦beta\n  > 3 | gamma⟧ delta\n    4 | last\n"
	if out.String() != want {
		t.Fatalf("snippet:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestByteIndexAtUTF16Offset(t *testing.T) {
	value := "a😀b"
	tests := []struct {
		offset int
		index  int
		ok     bool
	}{
		{offset: -1},
		{offset: 0, index: 0, ok: true},
		{offset: 1, index: 1, ok: true},
		{offset: 2},
		{offset: 3, index: 5, ok: true},
		{offset: 4, index: 6, ok: true},
		{offset: 5},
	}
	for _, test := range tests {
		index, ok := byteIndexAtUTF16Offset(value, test.offset)
		if ok != test.ok || index != test.index {
			t.Errorf("offset %d: index=%d ok=%t, want index=%d ok=%t", test.offset, index, ok, test.index, test.ok)
		}
	}
}

func TestLocateCommentAnchorRejectsStaleSelection(t *testing.T) {
	anchor := CommentAnchor{Path: "main.tex", OffsetUTF16: 2, Text: "old"}
	got, snippet, ok := locateCommentAnchor(anchor, []byte("a new value"))
	if ok || got.Line != 0 || len(snippet.Lines) != 0 {
		t.Fatalf("got=%#v snippet=%#v ok=%t", got, snippet, ok)
	}
}

func TestLocateCommentAnchorHandlesCRLFWithoutNormalizing(t *testing.T) {
	anchor := CommentAnchor{Path: "main.tex", OffsetUTF16: 3, Text: "b"}
	got, _, ok := locateCommentAnchor(anchor, []byte("a\r\nb"))
	if !ok || got.Line != 2 || got.Column != 1 {
		t.Fatalf("got=%#v ok=%t", got, ok)
	}
}

func TestMarkedSnippetTextEscapesTerminalControls(t *testing.T) {
	line := commentSnippetLine{
		Text:            "a\tb\x1b[31m",
		Selected:        true,
		SelectionStart:  3,
		SelectionEnd:    4,
		OpensSelection:  true,
		ClosesSelection: true,
	}
	if got, want := markedSnippetText(line), `a\tb⟦\u001B⟧[31m`; got != want {
		t.Fatalf("marked text = %q, want %q", got, want)
	}
}
