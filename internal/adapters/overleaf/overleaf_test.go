package overleaf

import (
	"archive/zip"
	"bytes"
	"context"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ovl/internal/domain"
)

func TestBackendListUsesCookieAndZip(t *testing.T) {
	zipBody := zipBytes(t, map[string]string{"main.tex": "hello\n"})
	var gotCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		if r.URL.Path != "/project/p1/download/zip" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(zipBody)
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "session-value", server.Client())
	got, err := backend.List(context.Background())
	must(t, err)
	if got["main.tex"].SHA256 == "" || string(got["main.tex"].Content) != "hello\n" {
		t.Fatalf("unexpected snapshot: %#v", got["main.tex"])
	}
	if gotCookie != "overleaf_session2=session-value" {
		t.Fatalf("unexpected cookie header: %q", gotCookie)
	}
}

func TestBackendPutUploadsWithCSRFAndFolderID(t *testing.T) {
	projectMeta := html.EscapeString(`{"rootFolder":[{"_id":"root-id","name":"root","docs":[],"fileRefs":[],"folders":[]}]}`)
	var sawUpload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-project" content="`+projectMeta+`">`)
		case "/project/p1/upload":
			sawUpload = true
			if r.URL.Query().Get("folder_id") != "root-id" {
				t.Fatalf("folder_id = %q", r.URL.Query().Get("folder_id"))
			}
			if r.Header.Get("X-Csrf-Token") != "csrf-token" {
				t.Fatalf("csrf = %q", r.Header.Get("X-Csrf-Token"))
			}
			must(t, r.ParseMultipartForm(1<<20))
			if r.FormValue("targetFolderId") != "root-id" {
				t.Fatalf("targetFolderId = %q", r.FormValue("targetFolderId"))
			}
			if r.FormValue("name") != "main.tex" {
				t.Fatalf("name = %q", r.FormValue("name"))
			}
			if r.FormValue("type") != "text/x-tex" {
				t.Fatalf("type = %q", r.FormValue("type"))
			}
			file, _, err := r.FormFile("qqfile")
			must(t, err)
			defer file.Close()
			body, err := io.ReadAll(file)
			must(t, err)
			if string(body) != "local\n" {
				t.Fatalf("uploaded body = %q", string(body))
			}
			_, _ = io.WriteString(w, `{"success":true,"entity_id":"uploaded-doc-id","entity_type":"doc"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "overleaf_session2=session-value", server.Client())
	state, err := backend.Put(context.Background(), domain.FileSnapshot{Path: "main.tex", Type: "text", SHA256: "sha", Size: 6, Content: []byte("local\n")})
	must(t, err)
	if !sawUpload {
		t.Fatal("upload endpoint was not called")
	}
	if state.RemoteID != "doc:uploaded-doc-id" {
		t.Fatalf("RemoteID = %q", state.RemoteID)
	}
}

func TestBackendPutFallsBackToEntitiesAndComputedRoot(t *testing.T) {
	projectID := "1234567890abcdef00000002"
	var deleted bool
	var uploaded bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/" + projectID:
			_, _ = io.WriteString(w, `<html>No project tree here</html>`)
		case "/project/" + projectID + "/entities":
			_, _ = io.WriteString(w, `{"entities":[{"path":"main.tex","type":"doc","_id":"doc-id"}]}`)
		case "/project/" + projectID + "/doc/doc-id":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		case "/project/" + projectID + "/upload":
			uploaded = true
			if r.URL.Query().Get("folder_id") != "1234567890abcdef00000001" {
				t.Fatalf("folder_id = %q", r.URL.Query().Get("folder_id"))
			}
			_, _ = io.WriteString(w, `{"success":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, projectID, "session-value", server.Client())
	_, err := backend.Put(context.Background(), domain.FileSnapshot{Path: "main.tex", Content: []byte("local\n")})
	must(t, err)
	if !deleted || !uploaded {
		t.Fatalf("deleted=%v uploaded=%v", deleted, uploaded)
	}
}

func TestBackendDeleteFolderFromProjectTree(t *testing.T) {
	projectMeta := html.EscapeString(`{"rootFolder":[{"_id":"root-id","name":"root","docs":[],"fileRefs":[],"folders":[{"_id":"folder-id","name":"chapters","docs":[],"fileRefs":[],"folders":[]}]}]}`)
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-project" content="`+projectMeta+`">`)
		case "/project/p1/entities":
			_, _ = io.WriteString(w, `{"entities":[]}`)
		case "/project/p1/folder/folder-id":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "session-value", server.Client())
	must(t, backend.Delete(context.Background(), "chapters/", domain.FileState{}))
	if !deleted {
		t.Fatal("folder delete endpoint was not called")
	}
}

func TestBackendDeletePrefersProjectTreeEntityOverEntitiesEndpoint(t *testing.T) {
	projectMeta := html.EscapeString(`{"rootFolder":[{"_id":"root-id","name":"root","docs":[{"_id":"tree-doc-id","name":"main.tex"}],"fileRefs":[],"folders":[]}]}`)
	var deletedPath string
	var deleteBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-project" content="`+projectMeta+`">`)
		case "/project/p1/entities":
			_, _ = io.WriteString(w, `{"entities":[{"path":"main.tex","type":"doc","_id":"api-doc-id"}]}`)
		case "/project/p1/doc/tree-doc-id":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s", r.Method)
			}
			body, err := io.ReadAll(r.Body)
			must(t, err)
			deletedPath = r.URL.Path
			deleteBody = string(body)
			w.WriteHeader(http.StatusNoContent)
		case "/project/p1/doc/api-doc-id":
			t.Fatal("delete used /entities id instead of project tree id")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "session-value", server.Client())
	must(t, backend.Delete(context.Background(), "main.tex", domain.FileState{}))
	if deletedPath != "/project/p1/doc/tree-doc-id" {
		t.Fatalf("deletedPath = %q", deletedPath)
	}
	if !strings.Contains(deleteBody, "_csrf=csrf-token") {
		t.Fatalf("delete body = %q", deleteBody)
	}
}

func TestBackendDeleteParsesRootFolderObjectAndIDAlias(t *testing.T) {
	projectMeta := html.EscapeString(`{"rootFolder":{"_id":"root-id","name":"root","docs":[{"id":"doc-id","name":"test.txt"}],"fileRefs":[],"folders":[]}}`)
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-project" content="`+projectMeta+`">`)
		case "/project/p1/entities":
			_, _ = io.WriteString(w, `{"entities":[]}`)
		case "/project/p1/doc/doc-id":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "session-value", server.Client())
	must(t, backend.Delete(context.Background(), "test.txt", domain.FileState{}))
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
}

func TestBackendDeleteParsesEntityAliases(t *testing.T) {
	projectMeta := html.EscapeString(`{"rootFolder":{"_id":"root-id","name":"root","docs":[],"fileRefs":[],"folders":[]}}`)
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-project" content="`+projectMeta+`">`)
		case "/project/p1/entities":
			_, _ = io.WriteString(w, `{"entities":[{"pathname":"/test.txt","entityType":"document","id":"doc-id"}]}`)
		case "/project/p1/doc/doc-id":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "session-value", server.Client())
	must(t, backend.Delete(context.Background(), "test.txt", domain.FileState{}))
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
}

func TestBackendDeleteUsesWSURLForSocketProjectTree(t *testing.T) {
	var deleted bool
	var sawSocketHandshake bool
	var sawDefaultSocket bool
	socketServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/realtime/1/":
			sawSocketHandshake = true
			_, _ = io.WriteString(w, "sid:60:60:xhr-polling")
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/realtime/1/xhr-polling/sid"):
			payload := `5:::` + `{"name":"joinProjectResponse","args":[{"project":{"rootFolder":[{"_id":"root-id","name":"root","docs":[{"_id":"doc-id","name":"test.txt"}],"fileRefs":[],"folders":[]}]}}]}`
			_, _ = io.WriteString(w, payload)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/realtime/1/xhr-polling/sid"):
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(socketServer.Close)
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-wsUrl" content="`+socketServer.URL+`/realtime">`)
		case "/project/p1/entities":
			_, _ = io.WriteString(w, `{"entities":[]}`)
		case "/project/p1/doc/doc-id":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		case "/socket.io/1/":
			sawDefaultSocket = true
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(webServer.Close)

	backend := New(webServer.URL, "p1", "session-value", webServer.Client())
	must(t, backend.Delete(context.Background(), "test.txt", domain.FileState{}))
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
	if !sawSocketHandshake {
		t.Fatal("custom socket endpoint was not used")
	}
	if sawDefaultSocket {
		t.Fatal("default socket endpoint was used despite ol-wsUrl")
	}
}

func TestBackendDeleteUsesLegacyWebSocketProjectTree(t *testing.T) {
	var deleted bool
	var sawWebSocket bool
	var webOrigin string
	var webURL string
	socketServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/socket.io/1/":
			_, _ = io.WriteString(w, "sid:60:60:websocket,xhr-polling")
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/socket.io/1/websocket/sid"):
			sawWebSocket = true
			webOrigin = r.Header.Get("Origin")
			if r.URL.Query().Get("t") != "" {
				t.Fatalf("websocket URL unexpectedly included t query: %s", r.URL.RawQuery)
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("response writer does not support hijacking")
			}
			conn, rw, err := hijacker.Hijack()
			must(t, err)
			defer conn.Close()
			_, _ = io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			must(t, rw.Flush())
			writeServerWebSocketText(t, rw, "1::")
			payload := `5:::` + `{"name":"joinProjectResponse","args":[{"project":{"rootFolder":[{"_id":"root-id","name":"root","docs":[{"_id":"doc-id","name":"test.txt"}],"fileRefs":[],"folders":[]}]}}]}`
			writeServerWebSocketText(t, rw, payload)
			must(t, rw.Flush())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(socketServer.Close)
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-wsUrl" content="`+socketServer.URL+`/socket.io">`)
		case "/project/p1/entities":
			_, _ = io.WriteString(w, `{"entities":[]}`)
		case "/project/p1/doc/doc-id":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(webServer.Close)
	webURL = webServer.URL

	backend := New(webServer.URL, "p1", "session-value", webServer.Client())
	must(t, backend.Delete(context.Background(), "test.txt", domain.FileState{}))
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
	if !sawWebSocket {
		t.Fatal("legacy websocket transport was not used")
	}
	if webOrigin != webURL {
		t.Fatalf("websocket origin = %q, want %q", webOrigin, webURL)
	}
}

func TestBackendDeleteUsesLegacyXHRPollingProjectTree(t *testing.T) {
	var deleted bool
	var unexpectedClientPacket string
	var sawStickyCookie bool
	polls := 0
	socketServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/socket.io/1/":
			http.SetCookie(w, &http.Cookie{Name: "io", Value: "sticky"})
			_, _ = io.WriteString(w, "sid:60:60:xhr-polling")
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/socket.io/1/xhr-polling/sid"):
			if strings.Contains(r.Header.Get("Cookie"), "io=sticky") {
				sawStickyCookie = true
			} else {
				_, _ = io.WriteString(w, "7:::1+0")
				return
			}
			polls++
			if polls == 1 {
				_, _ = io.WriteString(w, "1::")
				return
			}
			payload := `5:::` + `{"name":"joinProjectResponse","args":[{"project":{"rootFolder":[{"_id":"root-id","name":"root","docs":[{"_id":"doc-id","name":"test.txt"}],"fileRefs":[],"folders":[]}]}}]}`
			_, _ = io.WriteString(w, payload)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/socket.io/1/xhr-polling/sid"):
			body, err := io.ReadAll(r.Body)
			must(t, err)
			if string(body) != "0::" {
				unexpectedClientPacket = string(body)
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(socketServer.Close)
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-wsUrl" content="`+socketServer.URL+`/socket.io">`)
		case "/project/p1/entities":
			_, _ = io.WriteString(w, `{"entities":[]}`)
		case "/project/p1/doc/doc-id":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(webServer.Close)

	backend := New(webServer.URL, "p1", "session-value", webServer.Client())
	must(t, backend.Delete(context.Background(), "test.txt", domain.FileState{}))
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
	if polls < 2 {
		t.Fatalf("expected at least two polls, got %d", polls)
	}
	if unexpectedClientPacket != "" {
		t.Fatalf("unexpected client packet before server response: %q", unexpectedClientPacket)
	}
	if !sawStickyCookie {
		t.Fatal("sticky socket cookie from handshake was not sent to polling request")
	}
}

func TestBackendDeleteUsesEngineIOSocketProjectTree(t *testing.T) {
	var deleted bool
	var sawOpen bool
	var sawConnect bool
	socketServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/socket.io/" || r.URL.Query().Get("EIO") != "4" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("sid") == "" {
				sawOpen = true
				_, _ = io.WriteString(w, `0{"sid":"engine-sid","upgrades":[],"pingInterval":25000,"pingTimeout":20000}`)
				return
			}
			payload := `42["joinProjectResponse",{"project":{"rootFolder":[{"_id":"root-id","name":"root","docs":[{"_id":"doc-id","name":"test.txt"}],"fileRefs":[],"folders":[]}]}}]`
			_, _ = io.WriteString(w, payload)
		case http.MethodPost:
			body, err := io.ReadAll(r.Body)
			must(t, err)
			if string(body) == "40" {
				sawConnect = true
			}
			_, _ = io.WriteString(w, "ok")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(socketServer.Close)
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-wsUrl" content="`+socketServer.URL+`/socket.io">`)
		case "/project/p1/entities":
			_, _ = io.WriteString(w, `{"entities":[]}`)
		case "/project/p1/doc/doc-id":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(webServer.Close)

	backend := New(webServer.URL, "p1", "session-value", webServer.Client())
	must(t, backend.Delete(context.Background(), "test.txt", domain.FileState{}))
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
	if !sawOpen || !sawConnect {
		t.Fatalf("engine.io handshake incomplete: open=%v connect=%v", sawOpen, sawConnect)
	}
}

func TestBackendDeleteParsesLengthPrefixedEngineIOHandshake(t *testing.T) {
	var deleted bool
	var sawLengthPrefixedConnect bool
	socketServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/socket.io/" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("EIO") == "4" {
			http.Error(w, "unsupported", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("sid") == "" {
				_, _ = io.WriteString(w, `97:0{"sid":"engine-sid","upgrades":[],"pingInterval":25000,"pingTimeout":20000}2:40`)
				return
			}
			payload := `42["joinProjectResponse",{"project":{"rootFolder":[{"_id":"root-id","name":"root","docs":[{"_id":"doc-id","name":"test.txt"}],"fileRefs":[],"folders":[]}]}}]`
			_, _ = io.WriteString(w, payload)
		case http.MethodPost:
			body, err := io.ReadAll(r.Body)
			must(t, err)
			if string(body) == "2:40" {
				sawLengthPrefixedConnect = true
			}
			_, _ = io.WriteString(w, "ok")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(socketServer.Close)
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-wsUrl" content="`+socketServer.URL+`/socket.io">`)
		case "/project/p1/entities":
			_, _ = io.WriteString(w, `{"entities":[]}`)
		case "/project/p1/doc/doc-id":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(webServer.Close)

	backend := New(webServer.URL, "p1", "session-value", webServer.Client())
	must(t, backend.Delete(context.Background(), "test.txt", domain.FileState{}))
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
	if !sawLengthPrefixedConnect {
		t.Fatal("EIO=3 connect payload was not length-prefixed")
	}
}

func TestBackendDeleteMissingEntityReturnsError(t *testing.T) {
	projectMeta := html.EscapeString(`{"rootFolder":[{"_id":"root-id","name":"root","docs":[],"fileRefs":[],"folders":[]}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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
	err := backend.Delete(context.Background(), "missing.tex", domain.FileState{})
	if err == nil || !strings.Contains(err.Error(), "remote entity missing.tex not found") {
		t.Fatalf("expected missing entity error, got %v", err)
	}

	must(t, backend.Delete(context.Background(), "missing-dir/", domain.FileState{}))
}

func TestBackendDeleteUsesStateRemoteIDWhenLookupIsEmpty(t *testing.T) {
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1":
			_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
		case "/project/p1/entities":
			_, _ = io.WriteString(w, `{"entities":[]}`)
		case "/project/p1/doc/uploaded-doc-id":
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := New(server.URL, "p1", "session-value", server.Client())
	must(t, backend.Delete(context.Background(), "test.txt", domain.FileState{RemoteID: "doc:uploaded-doc-id"}))
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
}

func TestDecodeSocketPayload(t *testing.T) {
	got := decodeSocketPayload("\ufffd3\ufffd2::\ufffd6\ufffd5:::{}")
	if len(got) != 2 || got[0] != "2::" || got[1] != "5:::{}" {
		t.Fatalf("unexpected packets: %#v", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func writeServerWebSocketText(t *testing.T, w io.Writer, payload string) {
	t.Helper()
	header := []byte{0x81}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, byte(length))
	case length <= 0xffff:
		header = append(header, 126, byte(length>>8), byte(length))
	default:
		t.Fatalf("payload too large: %d", length)
	}
	if _, err := w.Write(header); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, payload); err != nil {
		t.Fatal(err)
	}
}

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
