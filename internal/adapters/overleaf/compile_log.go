package overleaf

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"ovl/internal/domain"
)

const (
	maxCompileLogDiagnostics  = 100
	maxCompileLogContextLines = 8
	maxCompileLogContextScan  = 24
	maxCompileLogLineBytes    = 16 * 1024
)

// parseCompileErrors extracts the error forms emitted by TeX itself and by
// Overleaf's file-line-error mode. It deliberately stays small: output.log is
// not a stable API, and unfamiliar lines are better left out than mislabeled.
func parseCompileErrors(log []byte) []domain.CompileError {
	lines := bytes.Split(log, []byte{'\n'})
	diagnostics := make([]domain.CompileError, 0)
	seen := make(map[string]struct{})

	for i, rawLine := range lines {
		diagnostic, ok, isFatal := parseCompileLogHeader(rawLine)
		if !ok {
			continue
		}
		if isFatal {
			continue
		}
		diagnostic.Context, diagnostic.Line = compileLogContext(lines, i+1, diagnostic.Line)

		key := compileLogDiagnosticKey(diagnostic)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}

		if len(diagnostics) == maxCompileLogDiagnostics {
			break
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	return diagnostics
}

func parseCompileLogHeader(rawLine []byte) (domain.CompileError, bool, bool) {
	line := strings.TrimRight(compileLogLine(rawLine), " \t")
	if strings.TrimSpace(line) == "" {
		return domain.CompileError{}, false, false
	}

	if line == "Runaway argument?" {
		return domain.CompileError{Message: line}, true, false
	}

	if strings.HasPrefix(line, "!") {
		message := strings.TrimSpace(strings.TrimPrefix(line, "!"))
		if message == "" {
			return domain.CompileError{}, false, false
		}
		if isCompileLogFatalSentinel(message) {
			return domain.CompileError{
				Message: "Fatal error occurred, no output PDF file produced!",
			}, true, true
		}
		return domain.CompileError{Message: message}, true, false
	}

	if diagnostic, ok := parseCompileLogFileLine(line); ok {
		return diagnostic, true, false
	}
	return domain.CompileError{}, false, false
}

func parseCompileLogFileLine(line string) (domain.CompileError, bool) {
	if line == "" || (line[0] != '.' && line[0] != '/') {
		return domain.CompileError{}, false
	}
	// The separator is :<digits>: followed by whitespace. Taking the first
	// such separator avoids treating either a numeric path component or a
	// later :<digits>: sequence in the error message as the source line.
	for fileEnd := 1; fileEnd < len(line); fileEnd++ {
		if line[fileEnd] != ':' {
			continue
		}
		lineEnd := fileEnd + 1
		for lineEnd < len(line) && line[lineEnd] >= '0' && line[lineEnd] <= '9' {
			lineEnd++
		}
		if lineEnd == fileEnd+1 || lineEnd+1 >= len(line) || line[lineEnd] != ':' ||
			(line[lineEnd+1] != ' ' && line[lineEnd+1] != '\t') {
			continue
		}
		lineNumber, err := strconv.Atoi(line[fileEnd+1 : lineEnd])
		message := strings.TrimSpace(line[lineEnd+1:])
		if err == nil && lineNumber > 0 && message != "" {
			return domain.CompileError{
				Message: message,
				File:    line[:fileEnd],
				Line:    lineNumber,
			}, true
		}
	}
	return domain.CompileError{}, false
}

func compileLogContext(lines [][]byte, start, lineNumber int) (string, int) {
	context := make([]string, 0, maxCompileLogContextLines)
	for i, scanned := start, 0; i < len(lines) && scanned < maxCompileLogContextScan; i, scanned = i+1, scanned+1 {
		line := compileLogLine(lines[i])
		if _, isHeader, _ := parseCompileLogHeader(lines[i]); isHeader {
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if parsedLine, ok := compileLogSourceLine(line); ok && lineNumber == 0 {
			lineNumber = parsedLine
		}
		if len(context) < maxCompileLogContextLines {
			context = append(context, strings.TrimRight(line, " \t"))
		}
	}
	return strings.Join(context, "\n"), lineNumber
}

func compileLogSourceLine(line string) (int, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "l.") {
		return 0, false
	}
	rest := line[len("l."):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 || (end < len(rest) && rest[end] != ' ' && rest[end] != '\t') {
		return 0, false
	}
	lineNumber, err := strconv.Atoi(rest[:end])
	if err != nil || lineNumber <= 0 {
		return 0, false
	}
	return lineNumber, true
}

func compileLogDiagnosticKey(diagnostic domain.CompileError) string {
	message := strings.Join(strings.Fields(diagnostic.Message), " ")
	context := strings.Join(strings.Fields(diagnostic.Context), " ")
	return diagnostic.File + "\x00" + strconv.Itoa(diagnostic.Line) + "\x00" + message + "\x00" + context
}

func isCompileLogFatalSentinel(message string) bool {
	message = strings.TrimSpace(message)
	message = strings.TrimLeft(message, "=> ")
	return strings.EqualFold(message, "Fatal error occurred, no output PDF file produced!")
}

func compileLogLine(rawLine []byte) string {
	rawLine = bytes.TrimSuffix(rawLine, []byte{'\r'})
	suffix := ""
	if len(rawLine) > maxCompileLogLineBytes {
		rawLine = rawLine[:maxCompileLogLineBytes]
		suffix = "…"
	}
	return sanitizeCompileLogLine(strings.ToValidUTF8(string(rawLine), "\uFFFD")) + suffix
}

func sanitizeCompileLogLine(line string) string {
	var sanitized strings.Builder
	for _, char := range line {
		if char == '\t' || (!unicode.IsControl(char) && !unicode.Is(unicode.Cf, char)) {
			sanitized.WriteRune(char)
			continue
		}
		fmt.Fprintf(&sanitized, "\\u{%04X}", char)
	}
	return sanitized.String()
}

func sanitizeCompileLogText(text []byte) string {
	lines := bytes.Split(text, []byte{'\n'})
	sanitized := make([]string, len(lines))
	for i, line := range lines {
		sanitized[i] = compileLogLine(line)
	}
	return strings.Join(sanitized, "\n")
}
