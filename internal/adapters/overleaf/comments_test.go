package overleaf

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBackendListCommentsJoinsThreadsRangesAndProjectTree(t *testing.T) {
	projectMeta := html.EscapeString(`{"rootFolder":[{"_id":"root-id","name":"root","docs":[],"fileRefs":[],"folders":[{"_id":"folder-id","name":"chapters","docs":[{"_id":"doc-id","name":"main.tex"}],"fileRefs":[],"folders":[]}]}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project/p1/threads":
			_, _ = io.WriteString(w, `{
                "detached":{"messages":[{"id":"m2","content":"Already handled","timestamp":"2026-10-15T08:27:38Z","user_id":"u2","user":{"email":"reviewer@example.com"}}],"resolved":true,"resolved_at":"2026-10-15T09:00:00Z"},
                "anchored":{"messages":[{"id":"m1","content":"Please clarify","timestamp":1700000000123,"user_id":"u1","user":{"id":"u1","first_name":"Ada","last_name":"Lovelace","email":"ada@example.com"}}]}
            }`)
		case "/project/p1/ranges":
			_, _ = io.WriteString(w, `[{"id":"doc-id","ranges":{"comments":[{"id":"anchored","op":{"c":"ambiguous phrase","p":42,"t":"anchored"}}],"changes":[]}}]`)
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-project" content="`+projectMeta+`">`)
		case "/project/p1/entities":
			_, _ = io.WriteString(w, `{"entities":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "session-value", server.Client())
	comments, err := backend.ListComments(context.Background())
	must(t, err)
	if len(comments) != 2 {
		t.Fatalf("comments = %#v", comments)
	}

	anchored := comments[0]
	if anchored.ID != "anchored" || anchored.Resolved {
		t.Fatalf("anchored thread = %#v", anchored)
	}
	if anchored.Anchor == nil || anchored.Anchor.Path != "chapters/main.tex" || anchored.Anchor.Offset != 42 || anchored.Anchor.Text != "ambiguous phrase" {
		t.Fatalf("anchor = %#v", anchored.Anchor)
	}
	if len(anchored.Messages) != 1 {
		t.Fatalf("messages = %#v", anchored.Messages)
	}
	message := anchored.Messages[0]
	if message.Author.ID != "u1" || message.Author.Name != "Ada Lovelace" || message.Author.Email != "ada@example.com" {
		t.Fatalf("author = %#v", message.Author)
	}
	if want := time.UnixMilli(1700000000123).UTC(); message.Timestamp == nil || !message.Timestamp.Equal(want) {
		t.Fatalf("timestamp = %s, want %s", message.Timestamp, want)
	}

	detached := comments[1]
	if detached.ID != "detached" || !detached.Resolved || detached.Anchor != nil {
		t.Fatalf("detached thread = %#v", detached)
	}
	if detached.ResolvedAt == nil || !detached.ResolvedAt.Equal(time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("resolved_at = %v", detached.ResolvedAt)
	}
}

func TestBackendListCommentsIgnoresRangeWhoseThreadIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project/p1/threads":
			_, _ = io.WriteString(w, `{}`)
		case "/project/p1/ranges":
			_, _ = io.WriteString(w, `[{"id":"unknown-doc","ranges":{"comments":[{"id":"range-id","op":{"c":"selection","p":7,"t":"missing-thread"}}]}}]`)
		default:
			http.Error(w, "tree unavailable", http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "session-value", server.Client())
	comments, err := backend.ListComments(context.Background())
	must(t, err)
	if len(comments) != 0 {
		t.Fatalf("comments = %#v", comments)
	}
}

func TestBackendListCommentsReportsThreadsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not allowed", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "session-value", server.Client())
	_, err := backend.ListComments(context.Background())
	if err == nil || !strings.Contains(err.Error(), "fetch comment threads failed: HTTP 403") {
		t.Fatalf("error = %v", err)
	}
}

func TestBackendListCommentsKeepsThreadsWhenRangesAreUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project/p1/threads":
			_, _ = io.WriteString(w, `{"thread-id":{"messages":[]}}`)
		case "/project/p1/ranges":
			http.Error(w, "not supported", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "session-value", server.Client())
	comments, err := backend.ListComments(context.Background())
	must(t, err)
	if len(comments) != 1 || comments[0].ID != "thread-id" || comments[0].Anchor != nil {
		t.Fatalf("comments = %#v", comments)
	}
}

func TestBackendListCommentsTreatsEmptySelectionAsDetached(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project/p1/threads":
			_, _ = io.WriteString(w, `{"thread-id":{"messages":[]}}`)
		case "/project/p1/ranges":
			_, _ = io.WriteString(w, `[{"id":"doc-id","ranges":{"comments":[{"id":"thread-id","op":{"c":"","p":12,"t":"thread-id"}}]}}]`)
		default:
			http.Error(w, "tree should not affect the result", http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "session-value", server.Client())
	comments, err := backend.ListComments(context.Background())
	must(t, err)
	if len(comments) != 1 || comments[0].Anchor != nil {
		t.Fatalf("comments = %#v", comments)
	}
}

func TestParseCommentTimeAcceptsMillisecondsAndRFC3339(t *testing.T) {
	got, ok := parseCommentTime([]byte(`1700000000123`))
	if !ok || !got.Equal(time.UnixMilli(1700000000123)) {
		t.Fatalf("millisecond timestamp = %s, ok = %t", got, ok)
	}
	got, ok = parseCommentTime([]byte(`"2026-10-15T08:27:38.123Z"`))
	if !ok || !got.Equal(time.Date(2026, 10, 15, 8, 27, 38, 123000000, time.UTC)) {
		t.Fatalf("RFC3339 timestamp = %s, ok = %t", got, ok)
	}
	got, ok = parseCommentTime([]byte(`"2026-10-15T17:27:38+09:00"`))
	if !ok || got.Location() != time.UTC || !got.Equal(time.Date(2026, 10, 15, 8, 27, 38, 0, time.UTC)) {
		t.Fatalf("offset timestamp = %s, location = %s, ok = %t", got, got.Location(), ok)
	}
}
