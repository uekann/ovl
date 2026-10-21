package overleaf

import (
	"bytes"
	"fmt"
	"path"
	"strconv"
	"strings"

	"ovl/internal/domain"
)

const (
	biberErrorMarker       = "> ERROR - "
	bibTeXLineMarker       = "---line "
	bibTeXReadingMarker    = "---while reading file "
	badCrossReferenceStart = "A bad cross reference---entry "
)

// parseBibliographyErrors extracts the deliberately small set of structured
// error forms emitted by Biber and BibTeX. Unrecognized text is left alone so
// warnings and echoed bibliography contents are not reported as errors.
func parseBibliographyErrors(log []byte) []domain.CompileError {
	lines := bytes.Split(log, []byte{'\n'})
	diagnostics := make([]domain.CompileError, 0)
	seen := make(map[string]struct{})

	for i := 0; i < len(lines) && len(diagnostics) < maxCompileLogDiagnostics; i++ {
		line := strings.TrimRight(compileLogLine(lines[i]), " \t")
		diagnostic, consumed, ok := parseBibliographyDiagnostic(lines, i, line)
		if !ok {
			continue
		}
		diagnostic.Context = bibliographyLogContext(lines, i+consumed)
		key := bibliographyDiagnosticKey(diagnostic)
		if _, duplicate := seen[key]; duplicate {
			i += consumed - 1
			continue
		}
		seen[key] = struct{}{}
		diagnostics = append(diagnostics, diagnostic)
		i += consumed - 1
	}
	summaryCount := 0
	for _, rawLine := range lines {
		if count, ok := bibTeXErrorSummaryCount(compileLogLine(rawLine)); ok && count > summaryCount {
			summaryCount = count
		}
	}
	if summaryCount > len(diagnostics) && len(diagnostics) < maxCompileLogDiagnostics {
		message := fmt.Sprintf("BibTeX: bibliography processor reported %d error message(s)", summaryCount)
		if len(diagnostics) > 0 {
			message += fmt.Sprintf("; only %d detailed message(s) could be parsed", len(diagnostics))
		}
		diagnostics = append(diagnostics, domain.CompileError{Message: message})
	}
	return diagnostics
}

func parseBibliographyDiagnostic(lines [][]byte, index int, line string) (domain.CompileError, int, bool) {
	if message, ok := parseBiberErrorLine(line); ok {
		return biberDiagnostic(message), 1, true
	}

	if diagnostic, markerOnly, ok := parseBibTeXLocatedError(line); ok {
		if markerOnly {
			message, found := precedingBibTeXMessage(lines, index)
			if !found {
				return domain.CompileError{}, 0, false
			}
			diagnostic.Message = "BibTeX: " + message
		} else if diagnostic.Message == "BibTeX: while executing" {
			message, found := precedingBibTeXMessage(lines, index)
			if found {
				diagnostic.Message = "BibTeX: " + message
			}
		}
		return diagnostic, 1, true
	}
	if diagnostic, ok := parseBibTeXReadingError(line); ok {
		return diagnostic, 1, true
	}

	if strings.HasPrefix(line, badCrossReferenceStart) && len(line) > len(badCrossReferenceStart) && index+1 < len(lines) {
		next := strings.TrimSpace(compileLogLine(lines[index+1]))
		if strings.HasPrefix(next, "refers to entry ") && strings.Contains(next, "which doesn't exist") {
			return domain.CompileError{Message: "BibTeX: " + line}, 1, true
		}
	}

	return domain.CompileError{}, 0, false
}

func parseBiberErrorLine(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") {
		return "", false
	}
	closeBracket := strings.IndexByte(line, ']')
	if closeBracket <= 1 || !allDecimalDigits(line[1:closeBracket]) {
		return "", false
	}
	rest := line[closeBracket+1:]
	severity := strings.Index(rest, "> ")
	if severity <= 0 || !strings.HasPrefix(rest[severity:], biberErrorMarker) {
		return "", false
	}
	message := strings.TrimSpace(rest[severity+len(biberErrorMarker):])
	return message, message != ""
}

func biberDiagnostic(message string) domain.CompileError {
	diagnostic := domain.CompileError{Message: "Biber: " + message}
	const subsystemPrefix = "BibTeX subsystem: "
	if !strings.HasPrefix(message, subsystemPrefix) {
		return diagnostic
	}

	detail := strings.TrimPrefix(message, subsystemPrefix)
	lineMarker := strings.LastIndex(detail, ", line ")
	if lineMarker <= 0 {
		return diagnostic
	}
	location := detail[:lineMarker]
	rest := detail[lineMarker+len(", line "):]
	messageMarker := strings.Index(rest, ", ")
	if messageMarker <= 0 {
		return diagnostic
	}
	lineNumber, err := strconv.Atoi(rest[:messageMarker])
	realMessage := strings.TrimSpace(rest[messageMarker+len(", "):])
	file := biberBibliographyFile(location)
	if err != nil || lineNumber <= 0 || realMessage == "" || file == "" {
		return diagnostic
	}
	diagnostic.File = file
	diagnostic.Line = lineNumber
	diagnostic.Message = "Biber: " + realMessage
	return diagnostic
}

func biberBibliographyFile(location string) string {
	base := path.Base(strings.TrimSpace(location))
	lower := strings.ToLower(base)
	bibEnd := strings.LastIndex(lower, ".bib")
	if bibEnd <= 0 {
		return ""
	}
	after := bibEnd + len(".bib")
	if after < len(base) && base[after] != '_' && base[after] != '.' {
		return ""
	}
	return base[:after]
}

func parseBibTeXLocatedError(line string) (domain.CompileError, bool, bool) {
	marker := strings.LastIndex(line, bibTeXLineMarker)
	if marker < 0 {
		return domain.CompileError{}, false, false
	}
	rest := line[marker+len(bibTeXLineMarker):]
	const fileMarker = " of file "
	fileStart := strings.Index(rest, fileMarker)
	if fileStart <= 0 {
		return domain.CompileError{}, false, false
	}
	lineNumber, err := strconv.Atoi(rest[:fileStart])
	file := strings.TrimSpace(rest[fileStart+len(fileMarker):])
	if err != nil || lineNumber <= 0 || file == "" {
		return domain.CompileError{}, false, false
	}
	message := strings.TrimSpace(line[:marker])
	if strings.HasPrefix(message, "Warning--") {
		return domain.CompileError{}, false, false
	}
	diagnostic := domain.CompileError{File: file, Line: lineNumber}
	if message == "" {
		return diagnostic, true, true
	}
	diagnostic.Message = "BibTeX: " + message
	return diagnostic, false, true
}

func parseBibTeXReadingError(line string) (domain.CompileError, bool) {
	marker := strings.LastIndex(line, bibTeXReadingMarker)
	if marker <= 0 {
		return domain.CompileError{}, false
	}
	message := strings.TrimSpace(line[:marker])
	file := strings.TrimSpace(line[marker+len(bibTeXReadingMarker):])
	if message == "" || file == "" || strings.HasPrefix(message, "Warning--") {
		return domain.CompileError{}, false
	}
	return domain.CompileError{File: file, Message: "BibTeX: " + message}, true
}

func bibTeXErrorSummaryCount(line string) (int, bool) {
	line = strings.TrimSpace(line)
	if len(line) < 2 || line[0] != '(' || line[len(line)-1] != ')' {
		return 0, false
	}
	fields := strings.Fields(line[1 : len(line)-1])
	if len(fields) != 5 || fields[0] != "There" || (fields[1] != "was" && fields[1] != "were") ||
		fields[3] != "error" || (fields[4] != "message" && fields[4] != "messages") {
		return 0, false
	}
	count, err := strconv.Atoi(fields[2])
	return count, err == nil && count > 0
}

func precedingBibTeXMessage(lines [][]byte, before int) (string, bool) {
	if before <= 0 {
		return "", false
	}
	line := strings.TrimSpace(compileLogLine(lines[before-1]))
	if line == "" || isBibliographyWarning(line) || isBiberRecord(line) || isBibliographyDiagnosticLine(line) {
		return "", false
	}
	return line, true
}

func bibliographyLogContext(lines [][]byte, start int) string {
	context := make([]string, 0, maxCompileLogContextLines)
	for i := start; i < len(lines) && len(context) < maxCompileLogContextLines; i++ {
		line := compileLogLine(lines[i])
		if strings.TrimSpace(line) == "" {
			break
		}
		trimmed := strings.TrimRight(line, " \t")
		if isBiberRecord(trimmed) || isBibliographyWarning(trimmed) || isBibTeXContextTerminator(trimmed) || isBibliographyDiagnosticLine(trimmed) {
			break
		}
		if i+1 < len(lines) {
			next := strings.TrimRight(compileLogLine(lines[i+1]), " \t")
			if _, markerOnly, ok := parseBibTeXLocatedError(next); ok && markerOnly {
				break
			}
		}
		context = append(context, trimmed)
	}
	return strings.Join(context, "\n")
}

func isBiberRecord(line string) bool {
	if !strings.HasPrefix(line, "[") {
		return false
	}
	closeBracket := strings.IndexByte(line, ']')
	if closeBracket <= 1 || !allDecimalDigits(line[1:closeBracket]) {
		return false
	}
	rest := line[closeBracket+1:]
	return strings.Index(rest, "> ") > 0
}

func isBibliographyWarning(line string) bool {
	return strings.HasPrefix(line, "Warning--")
}

func isBibTeXContextTerminator(line string) bool {
	return strings.HasPrefix(line, "I'm skipping whatever remains of this entry") ||
		strings.HasPrefix(line, "I'm skipping whatever remains of this command") ||
		strings.HasPrefix(line, "(There was ") ||
		strings.HasPrefix(line, "(There were ")
}

func isBibliographyDiagnosticLine(line string) bool {
	if _, ok := parseBiberErrorLine(line); ok {
		return true
	}
	if _, _, ok := parseBibTeXLocatedError(line); ok {
		return true
	}
	if _, ok := parseBibTeXReadingError(line); ok {
		return true
	}
	return strings.HasPrefix(line, badCrossReferenceStart)
}

func bibliographyDiagnosticKey(diagnostic domain.CompileError) string {
	message := strings.Join(strings.Fields(diagnostic.Message), " ")
	context := strings.Join(strings.Fields(diagnostic.Context), " ")
	return diagnostic.File + "\x00" + strconv.Itoa(diagnostic.Line) + "\x00" + message + "\x00" + context
}

func allDecimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}
