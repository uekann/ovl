package overleaf

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseBibliographyErrorsBiber(t *testing.T) {
	log := strings.Join([]string{
		"[10] Biber.pm:100> INFO - This is Biber",
		"[11] Utils.pm:200> WARN - This warning is not an error",
		"[12] Utils.pm:300> ERROR - BibTeX subsystem: /tmp/biber/project/references.bib_123.utf8, line 17, syntax error: found \",\", expected \"]=\"",
		"[13] Biber.pm:400> ERROR - Cannot find 'missing.bib'!",
	}, "\r\n")

	got := parseBibliographyErrors([]byte(log))
	if len(got) != 2 {
		t.Fatalf("diagnostics = %#v, want two Biber errors", got)
	}
	if got[0].File != "references.bib" || got[0].Line != 17 {
		t.Fatalf("subsystem location = %q:%d", got[0].File, got[0].Line)
	}
	if got[0].Message != `Biber: syntax error: found ",", expected "]="` {
		t.Fatalf("subsystem Message = %q", got[0].Message)
	}
	if got[1].File != "" || got[1].Line != 0 || got[1].Message != "Biber: Cannot find 'missing.bib'!" {
		t.Fatalf("generic Biber diagnostic = %#v", got[1])
	}
}

func TestParseBibliographyErrorsBibTeXLocatedForms(t *testing.T) {
	log := strings.Join([]string{
		"Repeated entry---line 12 of file ./refs:archive.bib",
		" : duplicate key",
		"",
		"Illegal, another \\bibstyle command",
		"---line 4 of file main.aux",
		" : \\bibstyle",
		"",
	}, "\n")

	got := parseBibliographyErrors([]byte(log))
	if len(got) != 2 {
		t.Fatalf("diagnostics = %#v, want two BibTeX errors", got)
	}
	if got[0].Message != "BibTeX: Repeated entry" || got[0].File != "./refs:archive.bib" || got[0].Line != 12 {
		t.Fatalf("inline diagnostic = %#v", got[0])
	}
	if got[0].Context != " : duplicate key" {
		t.Fatalf("inline Context = %q", got[0].Context)
	}
	if got[1].Message != "BibTeX: Illegal, another \\bibstyle command" || got[1].File != "main.aux" || got[1].Line != 4 {
		t.Fatalf("standalone-marker diagnostic = %#v", got[1])
	}
	if got[1].Context != " : \\bibstyle" {
		t.Fatalf("standalone-marker Context = %q", got[1].Context)
	}
}

func TestParseBibliographyErrorsBibTeXWhileReading(t *testing.T) {
	log := strings.Join([]string{
		`I found no \citation commands---while reading file output.aux`,
		`I found no \bibdata command---while reading file chapters/part.aux`,
		`I found no \bibstyle command---while reading file output.aux`,
		`I found no database files---while reading file output.aux`,
		`I found no style file---while reading file output.aux`,
	}, "\n")

	got := parseBibliographyErrors([]byte(log))
	if len(got) != 5 {
		t.Fatalf("diagnostics = %#v, want five BibTeX errors", got)
	}
	if got[0].File != "output.aux" || got[0].Message != `BibTeX: I found no \citation commands` {
		t.Fatalf("citation diagnostic = %#v", got[0])
	}
	if got[1].File != "chapters/part.aux" || got[1].Message != `BibTeX: I found no \bibdata command` {
		t.Fatalf("bibdata diagnostic = %#v", got[1])
	}
	if got[2].File != "output.aux" || got[2].Message != `BibTeX: I found no \bibstyle command` {
		t.Fatalf("bibstyle diagnostic = %#v", got[2])
	}
	if got[3].Message != "BibTeX: I found no database files" || got[4].Message != "BibTeX: I found no style file" {
		t.Fatalf("database/style diagnostics = %#v, %#v", got[3], got[4])
	}
}

func TestParseBibliographyErrorsBibTeXBSTError(t *testing.T) {
	log := "literal stack isn't empty\nwhile executing---line 42 of file custom.bst\n"

	got := parseBibliographyErrors([]byte(log))
	if len(got) != 1 {
		t.Fatalf("diagnostics = %#v", got)
	}
	want := "BibTeX: literal stack isn't empty"
	if got[0].File != "custom.bst" || got[0].Line != 42 || got[0].Message != want {
		t.Fatalf("BST diagnostic = %#v, want Message %q", got[0], want)
	}
}

func TestParseBibliographyErrorsUsesPositiveSummaryAsFallback(t *testing.T) {
	log := "unrecognized BibTeX detail\n(There were 2 error messages)\n"

	got := parseBibliographyErrors([]byte(log))
	if len(got) != 1 || got[0].Message != "BibTeX: bibliography processor reported 2 error message(s)" {
		t.Fatalf("diagnostics = %#v", got)
	}

	withDetail := "Repeated entry---line 12 of file refs.bib\n\n(There was 1 error message)\n"
	got = parseBibliographyErrors([]byte(withDetail))
	if len(got) != 1 || got[0].Message != "BibTeX: Repeated entry" {
		t.Fatalf("summary duplicated a concrete diagnostic: %#v", got)
	}

	if got := parseBibliographyErrors([]byte("(There were 0 error messages)\n")); len(got) != 0 {
		t.Fatalf("zero-error summary = %#v", got)
	}

	partial := "Repeated entry---line 12 of file refs.bib\n(There were 2 error messages)\n"
	got = parseBibliographyErrors([]byte(partial))
	if len(got) != 2 || !strings.Contains(got[1].Message, "only 1 detailed message") {
		t.Fatalf("partial summary = %#v", got)
	}
}

func TestParseBibliographyErrorsBadCrossReference(t *testing.T) {
	log := "A bad cross reference---entry \"child\"\r\n" +
		"refers to entry \"parent\", which doesn't exist\r\n" +
		"I couldn't open style file aa.bst\r\n" +
		"---line 31 of file output.aux\r\n"

	got := parseBibliographyErrors([]byte(log))
	if len(got) != 2 {
		t.Fatalf("diagnostics = %#v", got)
	}
	want := `BibTeX: A bad cross reference---entry "child"`
	if got[0].Message != want {
		t.Fatalf("Message = %q, want %q", got[0].Message, want)
	}
	if got[0].Context != `refers to entry "parent", which doesn't exist` {
		t.Fatalf("Context = %q", got[0].Context)
	}
	if got[1].Message != "BibTeX: I couldn't open style file aa.bst" || got[1].File != "output.aux" || got[1].Line != 31 {
		t.Fatalf("following marker diagnostic = %#v", got[1])
	}
}

func TestParseBibliographyErrorsIgnoresWarningsAndAmbiguousLines(t *testing.T) {
	log := strings.Join([]string{
		"[20] Biber.pm:100> WARN - Duplicate entry",
		"Warning--empty journal in example",
		"Warning--not an error---line 8 of file refs.bib",
		"[not-a-number] Biber.pm:100> ERROR - not structured",
		"message---line not-a-number of file refs.bib",
		"unrelated text",
		"",
		"---line 9 of file refs.bib",
		"A bad cross reference---entry \"child\"",
		"does not have the required second line",
	}, "\n")

	if got := parseBibliographyErrors([]byte(log)); len(got) != 0 {
		t.Fatalf("diagnostics = %#v, want ambiguous lines ignored", got)
	}
}

func TestParseBibliographyErrorsDeduplicatesExactErrorsButKeepsDifferentContext(t *testing.T) {
	log := strings.Join([]string{
		"Repeated entry---line 12 of file refs.bib",
		"context one",
		"",
		"Repeated   entry---line 12 of file refs.bib",
		"context one",
		"",
		"Repeated entry---line 12 of file refs.bib",
		"context two",
		"",
	}, "\n")

	got := parseBibliographyErrors([]byte(log))
	if len(got) != 2 {
		t.Fatalf("diagnostics = %#v, want exact duplicate removed", got)
	}
	if got[0].Context != "context one" || got[1].Context != "context two" {
		t.Fatalf("contexts = %q, %q", got[0].Context, got[1].Context)
	}
}

func TestParseBibliographyErrorsSanitizesControlsAndBoundsLines(t *testing.T) {
	huge := "[30] Biber.pm:100> ERROR - " + strings.Repeat("x", maxCompileLogLineBytes) + "\x1b[31m"
	got := parseBibliographyErrors([]byte(huge + "\n"))
	if len(got) != 1 {
		t.Fatalf("diagnostics = %#v", got)
	}
	if strings.ContainsRune(got[0].Message, '\x1b') {
		t.Fatalf("Message contains terminal escape: %q", got[0].Message)
	}
	if !strings.HasSuffix(got[0].Message, "…") {
		t.Fatalf("Message was not truncated: %q", got[0].Message[len(got[0].Message)-16:])
	}

	control := "[31] Biber.pm:100> ERROR - bad\x1b[31m color\x1b[0m"
	got = parseBibliographyErrors([]byte(control))
	if len(got) != 1 || !strings.Contains(got[0].Message, `\u{001B}[31m`) {
		t.Fatalf("sanitized diagnostic = %#v", got)
	}
}

func TestParseBibliographyErrorsDeduplicatesAndBoundsResultsAndContext(t *testing.T) {
	var log strings.Builder
	log.WriteString("[1] Tool.pm:1> ERROR - first\n")
	for i := 0; i < maxCompileLogContextLines+4; i++ {
		fmt.Fprintf(&log, "context %d\n", i)
	}
	log.WriteString("\n[1] Tool.pm:1> ERROR - first\n")
	for i := 1; i < maxCompileLogDiagnostics+20; i++ {
		fmt.Fprintf(&log, "\nerror %d---line %d of file refs.bib\n", i, i)
	}

	got := parseBibliographyErrors([]byte(log.String()))
	if len(got) != maxCompileLogDiagnostics {
		t.Fatalf("diagnostic count = %d, want %d", len(got), maxCompileLogDiagnostics)
	}
	if contextLines := strings.Count(got[0].Context, "\n") + 1; contextLines != maxCompileLogContextLines {
		t.Fatalf("context lines = %d, want %d", contextLines, maxCompileLogContextLines)
	}
}
