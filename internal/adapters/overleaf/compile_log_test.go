package overleaf

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseCompileErrorsBangErrorWithCRLF(t *testing.T) {
	log := strings.Join([]string{
		"This is pdfTeX",
		"! Undefined control sequence.",
		"l.4 \\doesnotexist",
		"              {argument}",
		"",
		"!  ==> Fatal error occurred, no output PDF file produced!",
	}, "\r\n")

	got := parseCompileErrors([]byte(log))
	if len(got) != 1 {
		t.Fatalf("diagnostics = %#v, want one non-sentinel error", got)
	}
	if got[0].Message != "Undefined control sequence." {
		t.Fatalf("Message = %q", got[0].Message)
	}
	if got[0].File != "" || got[0].Line != 4 {
		t.Fatalf("location = %q:%d, want line 4", got[0].File, got[0].Line)
	}
	wantContext := "l.4 \\doesnotexist\n              {argument}"
	if got[0].Context != wantContext {
		t.Fatalf("Context = %q, want %q", got[0].Context, wantContext)
	}
}

func TestParseCompileErrorsFileLineAndRunawayArgument(t *testing.T) {
	log := strings.Join([]string{
		"Runaway argument?",
		"{an argument that never ends",
		"./chapters/intro.tex:27: Paragraph ended before \\example was complete.",
		"<to be read again>",
		"                   \\par",
		"",
	}, "\n")

	got := parseCompileErrors([]byte(log))
	if len(got) != 2 {
		t.Fatalf("diagnostics = %#v, want two errors", got)
	}
	if got[0].Message != "Runaway argument?" {
		t.Fatalf("runaway diagnostic = %#v", got[0])
	}
	if want := "{an argument that never ends"; got[0].Context != want {
		t.Fatalf("runaway Context = %q, want %q", got[0].Context, want)
	}
	if got[1].File != "./chapters/intro.tex" || got[1].Line != 27 {
		t.Fatalf("file-line location = %q:%d", got[1].File, got[1].Line)
	}
	if got[1].Message != "Paragraph ended before \\example was complete." {
		t.Fatalf("file-line Message = %q", got[1].Message)
	}
	if strings.Count(got[1].Context, "\n") != 1 {
		t.Fatalf("file-line Context = %q", got[1].Context)
	}
}

func TestParseCompileErrorsFileLineAllowsAbsoluteAndColonPaths(t *testing.T) {
	log := strings.Join([]string{
		"/compile/project/main.tex:13: Undefined control sequence.",
		"./chapters/part:one.tex:21: Missing $ inserted.",
		"./chapters/part:2024:notes.tex:34: Extra }, or forgotten $.",
		"../shared/appendix.tex:55: File ended while scanning use of \\example.",
		"./main.tex:61: Package error: code:123: bad input.",
	}, "\n")

	got := parseCompileErrors([]byte(log))
	if len(got) != 5 {
		t.Fatalf("diagnostics = %#v", got)
	}
	if got[0].File != "/compile/project/main.tex" || got[0].Line != 13 {
		t.Fatalf("absolute location = %q:%d", got[0].File, got[0].Line)
	}
	if got[1].File != "./chapters/part:one.tex" || got[1].Line != 21 {
		t.Fatalf("colon-path location = %q:%d", got[1].File, got[1].Line)
	}
	if got[2].File != "./chapters/part:2024:notes.tex" || got[2].Line != 34 {
		t.Fatalf("numeric-colon-path location = %q:%d", got[2].File, got[2].Line)
	}
	if got[3].File != "../shared/appendix.tex" || got[3].Line != 55 {
		t.Fatalf("parent-relative location = %q:%d", got[3].File, got[3].Line)
	}
	if got[4].File != "./main.tex" || got[4].Line != 61 || got[4].Message != "Package error: code:123: bad input." {
		t.Fatalf("numeric-message location = %#v", got[4])
	}
}

func TestParseCompileErrorsPDFTeXEngineError(t *testing.T) {
	log := "!pdfTeX error: pdflatex (file broken.png): libpng: internal error\n"

	got := parseCompileErrors([]byte(log))
	if len(got) != 1 || got[0].Message != "pdfTeX error: pdflatex (file broken.png): libpng: internal error" {
		t.Fatalf("diagnostics = %#v", got)
	}
}

func TestParseCompileErrorsFindsSourceLineAfterBlankLine(t *testing.T) {
	log := "! LaTeX Error: Something failed.\n\nSee the manual for details.\n\nl.27 \\broken\n\n"

	got := parseCompileErrors([]byte(log))
	if len(got) != 1 || got[0].Line != 27 {
		t.Fatalf("diagnostics = %#v, want line after blank separators", got)
	}
	if got[0].Context != "See the manual for details.\nl.27 \\broken" {
		t.Fatalf("Context = %q", got[0].Context)
	}
}

func TestParseCompileErrorsDeduplicatesErrorsButKeepsDistinctLocations(t *testing.T) {
	log := strings.Join([]string{
		"./main.tex:2: Missing $ inserted.",
		"",
		"./main.tex:2:   Missing   $ inserted.",
		"",
		"./main.tex:8: Missing $ inserted.",
		"",
	}, "\n")

	got := parseCompileErrors([]byte(log))
	if len(got) != 2 {
		t.Fatalf("diagnostics = %#v, want duplicate removed and distinct line kept", got)
	}
	if got[0].Line != 2 || got[1].Line != 8 {
		t.Fatalf("lines = %d, %d", got[0].Line, got[1].Line)
	}
}

func TestParseCompileErrorsKeepsSameErrorWithDistinctContext(t *testing.T) {
	log := strings.Join([]string{
		"! Undefined control sequence.",
		"l.4 \\firstMissingCommand",
		"",
		"! Undefined control sequence.",
		"l.4 \\secondMissingCommand",
		"",
	}, "\n")

	got := parseCompileErrors([]byte(log))
	if len(got) != 2 {
		t.Fatalf("diagnostics = %#v, want distinct source contexts preserved", got)
	}
}

func TestParseCompileErrorsIgnoresFatalSentinel(t *testing.T) {
	log := "!  ==> Fatal error occurred, no output PDF file produced!\n" +
		"!  ==> Fatal error occurred, no output PDF file produced!\n"

	got := parseCompileErrors([]byte(log))
	if len(got) != 0 {
		t.Fatalf("diagnostics = %#v", got)
	}
}

func TestParseCompileErrorsDoesNotTreatIndentedSourceAsHeader(t *testing.T) {
	log := "  ! source text, not a TeX error header\n  Runaway argument?\n  ./main.tex:4: source text\n"

	got := parseCompileErrors([]byte(log))
	if len(got) != 0 {
		t.Fatalf("diagnostics = %#v", got)
	}
}

func TestParseCompileErrorsEscapesTerminalControls(t *testing.T) {
	log := []byte("! Bad ")
	log = append(log, 0x1b)
	log = append(log, []byte("]0;forged title")...)
	log = append(log, 0x07)
	log = append(log, []byte(".\ncontext\t")...)
	log = append(log, 0x1b)
	log = append(log, []byte("[31mred\n\n")...)

	got := parseCompileErrors(log)
	if len(got) != 1 {
		t.Fatalf("diagnostics = %#v", got)
	}
	if strings.ContainsAny(got[0].Message+got[0].Context, "\x1b\x07") {
		t.Fatalf("terminal controls were not escaped: %#v", got[0])
	}
	if !strings.Contains(got[0].Message, `\u{001B}`) || !strings.Contains(got[0].Message, `\u{0007}`) {
		t.Fatalf("escaped Message = %q", got[0].Message)
	}
	if !strings.Contains(got[0].Context, "context\t") || !strings.Contains(got[0].Context, `\u{001B}`) {
		t.Fatalf("escaped Context = %q", got[0].Context)
	}
}

func TestParseCompileErrorsBoundsContextAndDiagnosticCount(t *testing.T) {
	var log strings.Builder
	for i := 0; i < maxCompileLogContextLines+5; i++ {
		if i == 0 {
			log.WriteString("! First error.\n")
		}
		if i == maxCompileLogContextLines+2 {
			log.WriteString("l.77 \\lateSourceLine\n")
		} else {
			fmt.Fprintf(&log, "context %d\n", i)
		}
	}
	log.WriteString("\n")
	for i := 0; i < maxCompileLogDiagnostics+25; i++ {
		fmt.Fprintf(&log, "./file-%03d.tex:1: error %03d\n\n", i, i)
	}

	got := parseCompileErrors([]byte(log.String()))
	if len(got) != maxCompileLogDiagnostics {
		t.Fatalf("diagnostic count = %d, want %d", len(got), maxCompileLogDiagnostics)
	}
	if gotContextLines := strings.Count(got[0].Context, "\n") + 1; gotContextLines != maxCompileLogContextLines {
		t.Fatalf("context count = %d, want %d", gotContextLines, maxCompileLogContextLines)
	}
	if got[0].Line != 77 {
		t.Fatalf("Line = %d, want source line found beyond retained context", got[0].Line)
	}
}

func TestParseCompileErrorsHandlesHugeLine(t *testing.T) {
	hugeMessage := "! " + strings.Repeat("x", 1<<20)
	got := parseCompileErrors([]byte(hugeMessage + "\n"))
	if len(got) != 1 {
		t.Fatalf("diagnostics = %#v", got)
	}
	if len(got[0].Message) > maxCompileLogLineBytes+len("…") {
		t.Fatalf("huge message was not bounded: %d bytes", len(got[0].Message))
	}
	if !strings.HasSuffix(got[0].Message, "…") {
		t.Fatalf("truncated Message = %q", got[0].Message[len(got[0].Message)-10:])
	}
}
