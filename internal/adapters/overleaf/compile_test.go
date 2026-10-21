package overleaf

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const compileErrorLog = `This is pdfTeX, Version 3.141592653
! Undefined control sequence.
l.4 \doesnotexist
                  {}
`

type compileHTTPFixture struct {
	status             string
	withPDF            bool
	withLog            bool
	log                string
	logStatus          int
	withBLG            bool
	blg                string
	blgStatus          int
	validationProblems map[string]any
}

type compileHTTPState struct {
	mu sync.Mutex

	rootResourcePath string
	rootDocID        string
	compileEditorID  string
	compileCSRF      string
	logRequested     bool
	logCLSID         string
	logEditorID      string
	blgRequested     bool
	blgCLSID         string
	blgEditorID      string
}

type compileHTTPSnapshot struct {
	rootResourcePath string
	rootDocID        string
	compileEditorID  string
	compileCSRF      string
	logRequested     bool
	logCLSID         string
	logEditorID      string
	blgRequested     bool
	blgCLSID         string
	blgEditorID      string
}

func (s *compileHTTPState) snapshot() compileHTTPSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return compileHTTPSnapshot{
		rootResourcePath: s.rootResourcePath,
		rootDocID:        s.rootDocID,
		compileEditorID:  s.compileEditorID,
		compileCSRF:      s.compileCSRF,
		logRequested:     s.logRequested,
		logCLSID:         s.logCLSID,
		logEditorID:      s.logEditorID,
		blgRequested:     s.blgRequested,
		blgCLSID:         s.blgCLSID,
		blgEditorID:      s.blgEditorID,
	}
}

func newCompileHTTPServer(t *testing.T, fixture compileHTTPFixture) (*httptest.Server, *compileHTTPState) {
	t.Helper()
	state := &compileHTTPState{}
	projectMeta := html.EscapeString(`{"rootFolder":[{"_id":"root-id","name":"root","docs":[{"_id":"main-doc-id","name":"main.tex"}],"fileRefs":[],"folders":[]}]}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case r.Method == http.MethodGet && r.URL.Path == "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-project" content="`+projectMeta+`">`)
		case r.Method == http.MethodPost && r.URL.Path == "/project/p1/compile":
			var payload struct {
				RootResourcePath string `json:"rootResourcePath"`
				RootDocID        string `json:"rootDoc_id"`
				EditorID         string `json:"editorId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			state.mu.Lock()
			state.rootResourcePath = payload.RootResourcePath
			state.rootDocID = payload.RootDocID
			state.compileEditorID = payload.EditorID
			state.compileCSRF = r.Header.Get("X-Csrf-Token")
			state.mu.Unlock()

			outputFiles := make([]map[string]string, 0, 3)
			if fixture.withPDF {
				outputFiles = append(outputFiles, map[string]string{
					"path": "output.pdf",
					"url":  "/project/p1/build/build-1/output/output.pdf",
					"type": "pdf",
				})
			}
			if fixture.withLog {
				outputFiles = append(outputFiles, map[string]string{
					"path": "output.log",
					"url":  "/project/p1/build/build-1/output/output.log",
					"type": "log",
				})
			}
			if fixture.withBLG {
				outputFiles = append(outputFiles, map[string]string{
					"path": "output.blg",
					"url":  "/project/p1/build/build-1/output/output.blg",
					"type": "blg",
				})
			}
			response := map[string]any{
				"status":       fixture.status,
				"clsiServerId": "clsi-1",
				"outputFiles":  outputFiles,
			}
			if fixture.validationProblems != nil {
				response["validationProblems"] = fixture.validationProblems
			}
			_ = json.NewEncoder(w).Encode(response)
		case r.Method == http.MethodGet && r.URL.Path == "/project/p1/build/build-1/output/output.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = io.WriteString(w, "%PDF fake\n")
		case r.Method == http.MethodGet && r.URL.Path == "/project/p1/build/build-1/output/output.log":
			state.mu.Lock()
			state.logRequested = true
			state.logCLSID = r.URL.Query().Get("clsiserverid")
			state.logEditorID = r.URL.Query().Get("editorId")
			state.mu.Unlock()
			if fixture.logStatus >= http.StatusBadRequest {
				http.Error(w, "log unavailable", fixture.logStatus)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, fixture.log)
		case r.Method == http.MethodGet && r.URL.Path == "/project/p1/build/build-1/output/output.blg":
			state.mu.Lock()
			state.blgRequested = true
			state.blgCLSID = r.URL.Query().Get("clsiserverid")
			state.blgEditorID = r.URL.Query().Get("editorId")
			state.mu.Unlock()
			if fixture.blgStatus >= http.StatusBadRequest {
				http.Error(w, "bibliography log unavailable", fixture.blgStatus)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, fixture.blg)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, state
}

func TestBackendCompilePDFReportsErrorsFromSuccessfulCompileLog(t *testing.T) {
	server, state := newCompileHTTPServer(t, compileHTTPFixture{
		status:  "success",
		withPDF: true,
		withLog: true,
		log:     compileErrorLog,
	})
	backend := New(server.URL, "p1", "session-value", server.Client())

	result, err := backend.CompilePDF(context.Background(), "main.tex")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(result.PDF); got != "%PDF fake\n" {
		t.Fatalf("PDF = %q", got)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %#v, want one compile error", result.Errors)
	}
	if got := result.Errors[0].Message; !strings.Contains(got, "Undefined control sequence.") {
		t.Fatalf("error message = %q", got)
	}
	if got := result.Errors[0].Line; got != 4 {
		t.Fatalf("error line = %d, want 4", got)
	}

	requests := state.snapshot()
	if requests.rootResourcePath != "main.tex" || requests.rootDocID != "main-doc-id" {
		t.Fatalf("compile root = (%q, %q)", requests.rootResourcePath, requests.rootDocID)
	}
	if requests.compileCSRF != "csrf-token" {
		t.Fatalf("compile CSRF = %q", requests.compileCSRF)
	}
	if requests.compileEditorID == "" {
		t.Fatal("compile request has an empty editorId")
	}
	if !requests.logRequested {
		t.Fatal("output.log was not requested")
	}
	if requests.logCLSID != "clsi-1" {
		t.Fatalf("output.log clsiserverid = %q", requests.logCLSID)
	}
	if requests.logEditorID != requests.compileEditorID {
		t.Fatalf("output.log editorId = %q, compile editorId = %q", requests.logEditorID, requests.compileEditorID)
	}
}

func TestBackendCompilePDFIgnoresWarningsInCompileLog(t *testing.T) {
	server, _ := newCompileHTTPServer(t, compileHTTPFixture{
		status:  "success",
		withPDF: true,
		withLog: true,
		log:     "LaTeX Warning: Label(s) may have changed. Rerun to get cross-references right.\n",
	})
	backend := New(server.URL, "p1", "session-value", server.Client())

	result, err := backend.CompilePDF(context.Background(), "main.tex")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(result.PDF); got != "%PDF fake\n" {
		t.Fatalf("PDF = %q", got)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("Errors = %#v, want no compile errors", result.Errors)
	}
}

func TestBackendCompilePDFReportsErrorsFromBibliographyLog(t *testing.T) {
	server, state := newCompileHTTPServer(t, compileHTTPFixture{
		status:  "success",
		withPDF: true,
		withLog: true,
		withBLG: true,
		blg:     "[12] Utils.pm:300> ERROR - BibTeX subsystem: /tmp/project/references.bib_123.utf8, line 17, syntax error\n",
	})
	backend := New(server.URL, "p1", "session-value", server.Client())

	result, err := backend.CompilePDF(context.Background(), "main.tex")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(result.PDF); got != "%PDF fake\n" {
		t.Fatalf("PDF = %q", got)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %#v, want one bibliography error", result.Errors)
	}
	diagnostic := result.Errors[0]
	if diagnostic.File != "references.bib" || diagnostic.Line != 17 || diagnostic.Message != "Biber: syntax error" {
		t.Fatalf("bibliography diagnostic = %#v", diagnostic)
	}

	requests := state.snapshot()
	if !requests.blgRequested {
		t.Fatal("output.blg was not requested")
	}
	if requests.blgCLSID != "clsi-1" || requests.blgEditorID != requests.compileEditorID {
		t.Fatalf("output.blg query = (clsiserverid %q, editorId %q), compile editorId = %q", requests.blgCLSID, requests.blgEditorID, requests.compileEditorID)
	}
}

func TestBackendCompilePDFFailureReturnsPDFAndErrors(t *testing.T) {
	server, state := newCompileHTTPServer(t, compileHTTPFixture{
		status:  "failure",
		withPDF: true,
		withLog: true,
		log:     compileErrorLog,
	})
	backend := New(server.URL, "p1", "session-value", server.Client())

	result, err := backend.CompilePDF(context.Background(), "main.tex")
	if err == nil || !strings.Contains(err.Error(), "compile status is failure") {
		t.Fatalf("error = %v, want failure status", err)
	}
	if got := string(result.PDF); got != "%PDF fake\n" {
		t.Fatalf("PDF = %q, want PDF returned by failed compile", got)
	}
	if result.Status != "failure" {
		t.Fatalf("Status = %q, want failure", result.Status)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %#v, want one compile error", result.Errors)
	}
	if got := result.Errors[0].Message; !strings.Contains(got, "Undefined control sequence.") {
		t.Fatalf("error message = %q", got)
	}
	if got := result.Errors[0].Line; got != 4 {
		t.Fatalf("error line = %d, want 4", got)
	}
	if !state.snapshot().logRequested {
		t.Fatal("output.log was not requested for failed compile")
	}
}

func TestBackendCompilePDFFailureReportsMissingCompileLog(t *testing.T) {
	server, _ := newCompileHTTPServer(t, compileHTTPFixture{
		status:  "failure",
		withPDF: true,
		validationProblems: map[string]any{
			"compiler": "invalid compiler setting",
		},
	})
	backend := New(server.URL, "p1", "session-value", server.Client())

	result, err := backend.CompilePDF(context.Background(), "main.tex")
	if err == nil {
		t.Fatal("error = nil, want failed status and missing compile-log error")
	}
	for _, want := range []string{"compile status is failure", "output.log was not returned", "compiler", "invalid compiler setting"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want it to contain %q", err, want)
		}
	}
	if got := string(result.PDF); got != "%PDF fake\n" {
		t.Fatalf("PDF = %q, want PDF returned by failed compile", got)
	}
	if result.Status != "failure" {
		t.Fatalf("Status = %q, want failure", result.Status)
	}
}

func TestBackendCompilePDFSuccessWithoutPDFPreservesCompileErrors(t *testing.T) {
	server, state := newCompileHTTPServer(t, compileHTTPFixture{
		status:  "success",
		withLog: true,
		log:     compileErrorLog,
	})
	backend := New(server.URL, "p1", "session-value", server.Client())

	result, err := backend.CompilePDF(context.Background(), "main.tex")
	if err == nil || !strings.Contains(err.Error(), "output.pdf was not returned") {
		t.Fatalf("error = %v, want missing PDF error", err)
	}
	if len(result.PDF) != 0 {
		t.Fatalf("PDF = %q, want no PDF", result.PDF)
	}
	if result.Status != "success" {
		t.Fatalf("Status = %q, want success", result.Status)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %#v, want one compile error", result.Errors)
	}
	if got := result.Errors[0].Message; !strings.Contains(got, "Undefined control sequence.") {
		t.Fatalf("error message = %q", got)
	}
	if got := result.Errors[0].Line; got != 4 {
		t.Fatalf("error line = %d, want 4", got)
	}
	if !state.snapshot().logRequested {
		t.Fatal("output.log was not requested before reporting the missing PDF")
	}
}

func TestBackendCompilePDFParsesErrorAtEndOfOversizedLog(t *testing.T) {
	const ordinaryLine = "ordinary compile output\n"
	oversizedLog := strings.Repeat(ordinaryLine, (1<<20)/len(ordinaryLine)+1) + compileErrorLog
	server, _ := newCompileHTTPServer(t, compileHTTPFixture{
		status:  "success",
		withPDF: true,
		withLog: true,
		log:     oversizedLog,
	})
	backend := New(server.URL, "p1", "session-value", server.Client())

	result, err := backend.CompilePDF(context.Background(), "main.tex")
	if err == nil || !strings.Contains(err.Error(), "output.log exceeds") {
		t.Fatalf("error = %v, want oversized compile-log error", err)
	}
	if got := string(result.PDF); got != "%PDF fake\n" {
		t.Fatalf("PDF = %q, want successfully downloaded PDF", got)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %#v, want trailing compile error", result.Errors)
	}
	if got := result.Errors[0].Message; !strings.Contains(got, "Undefined control sequence.") {
		t.Fatalf("error message = %q", got)
	}
	if got := result.Errors[0].Line; got != 4 {
		t.Fatalf("error line = %d, want 4", got)
	}
}

func TestBackendCompilePDFReturnsPDFWhenCompileLogDownloadFails(t *testing.T) {
	server, _ := newCompileHTTPServer(t, compileHTTPFixture{
		status:    "success",
		withPDF:   true,
		withLog:   true,
		logStatus: http.StatusInternalServerError,
	})
	backend := New(server.URL, "p1", "session-value", server.Client())

	result, err := backend.CompilePDF(context.Background(), "main.tex")
	if err == nil || !strings.Contains(err.Error(), "download compile log failed") {
		t.Fatalf("error = %v, want compile-log download failure", err)
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("error = %v, want HTTP status", err)
	}
	if got := string(result.PDF); got != "%PDF fake\n" {
		t.Fatalf("PDF = %q, want successfully downloaded PDF", got)
	}
}

func TestBackendCompilePDFReturnsPDFWhenCompileLogIsMissing(t *testing.T) {
	server, _ := newCompileHTTPServer(t, compileHTTPFixture{
		status:  "success",
		withPDF: true,
	})
	backend := New(server.URL, "p1", "session-value", server.Client())

	result, err := backend.CompilePDF(context.Background(), "main.tex")
	if err == nil || !strings.Contains(err.Error(), "output.log was not returned") {
		t.Fatalf("error = %v, want missing compile-log error", err)
	}
	if got := string(result.PDF); got != "%PDF fake\n" {
		t.Fatalf("PDF = %q, want successfully downloaded PDF", got)
	}
}

func TestCompileDownloadPathValidatesOriginAndCompileIdentity(t *testing.T) {
	backend := New("https://www.overleaf.com", "p1", "session-value", &http.Client{})

	valid := []string{
		"/project/p1/build/b1/output/output.pdf",
		"https://www.overleaf.com/project/p1/build/b1/output/output.pdf",
		"/project/p1/build/b1/output/output.pdf?clsiserverid=clsi-1&editorId=editor-1",
	}
	for _, outputURL := range valid {
		downloadPath, err := backend.compileDownloadPath(outputURL, "clsi-1", "editor-1")
		if err != nil {
			t.Fatalf("compileDownloadPath(%q): %v", outputURL, err)
		}
		parsed, err := url.Parse(downloadPath)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Query().Get("clsiserverid") != "clsi-1" || parsed.Query().Get("editorId") != "editor-1" {
			t.Fatalf("compileDownloadPath(%q) = %q", outputURL, downloadPath)
		}
	}

	unsafe := []string{
		"@attacker.example/stolen",
		"//attacker.example/stolen",
		"https://attacker.example/stolen",
		"https://user@www.overleaf.com/stolen",
		"/output.pdf?clsiserverid=other",
		"/output.pdf?editorId=other",
		"/output.pdf?editorId=editor-1&editorId=editor-1",
	}
	for _, outputURL := range unsafe {
		if _, err := backend.compileDownloadPath(outputURL, "clsi-1", "editor-1"); err == nil {
			t.Errorf("compileDownloadPath(%q) succeeded, want rejection", outputURL)
		}
	}
}

func TestCompileOutputRejectsInitialURLThatCouldChangeOrigin(t *testing.T) {
	var attackerHit atomic.Bool
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerHit.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(attacker.Close)

	origin := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(origin.Close)
	backend := New(origin.URL, "p1", "session-secret", origin.Client())
	injectedURL := "@" + strings.TrimPrefix(attacker.URL, "http://") + "/stolen"

	if _, err := backend.downloadCompileOutput(context.Background(), injectedURL, "clsi-1", "editor-1"); err == nil {
		t.Fatal("PDF download accepted an unsafe relative URL")
	} else if strings.Contains(err.Error(), "session-secret") {
		t.Fatalf("PDF error exposed the session cookie: %v", err)
	}
	if _, err := backend.downloadCompileTextOutput(context.Background(), compileOutputFile{Path: "output.log", URL: injectedURL}, "clsi-1", "editor-1"); err == nil {
		t.Fatal("log download accepted an unsafe relative URL")
	} else if strings.Contains(err.Error(), "session-secret") {
		t.Fatalf("log error exposed the session cookie: %v", err)
	}
	if attackerHit.Load() {
		t.Fatal("unsafe compile output URL reached the foreign origin")
	}
}

func TestCompileOutputRedirectsStayOnExactOrigin(t *testing.T) {
	var attackerHit atomic.Bool
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerHit.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(attacker.Close)

	var sameOriginCookie string
	var redirectOffOrigin atomic.Bool
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same-origin":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			sameOriginCookie = r.Header.Get("Cookie")
			_, _ = io.WriteString(w, "%PDF redirected\n")
		case "/off-origin":
			redirectOffOrigin.Store(true)
			http.Redirect(w, r, attacker.URL+"/stolen", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(origin.Close)
	backend := New(origin.URL, "p1", "session-secret", origin.Client())

	pdf, err := backend.downloadCompileOutput(context.Background(), "/same-origin", "clsi-1", "editor-1")
	if err != nil || string(pdf) != "%PDF redirected\n" {
		t.Fatalf("same-origin redirect = (%q, %v)", pdf, err)
	}
	if !strings.Contains(sameOriginCookie, "session-secret") {
		t.Fatalf("same-origin redirect cookie = %q", sameOriginCookie)
	}

	_, err = backend.downloadCompileOutput(context.Background(), "/off-origin", "clsi-1", "editor-1")
	if err == nil || !strings.Contains(err.Error(), errCompileOutputCrossOriginRedirect.Error()) {
		t.Fatalf("off-origin redirect error = %v", err)
	}
	if !redirectOffOrigin.Load() {
		t.Fatal("origin redirect endpoint was not requested")
	}
	if attackerHit.Load() {
		t.Fatal("compile output redirect reached the foreign origin")
	}
}
