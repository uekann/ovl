package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSafePushRefusesRemoteChange(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{"main.tex": "base\n"})
	must(t, os.WriteFile(filepath.Join(project, "main.tex"), []byte("local\n"), 0o644))
	remote.set("main.tex", "remote\n")

	err := Run(context.Background(), []string{"push"})
	if err == nil || !strings.Contains(err.Error(), "push refused") {
		t.Fatalf("expected push refusal, got %v", err)
	}
	if got := remote.get("main.tex"); got != "remote\n" {
		t.Fatalf("remote was overwritten: %q", got)
	}
}

func TestHelpFlags(t *testing.T) {
	must(t, Run(context.Background(), []string{"--help"}))
	must(t, Run(context.Background(), []string{"auth", "--help"}))
	must(t, Run(context.Background(), []string{"push", "--help"}))
}

func TestPushUploadsLocalOnlyChange(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{"main.tex": "base\n"})
	must(t, os.WriteFile(filepath.Join(project, "main.tex"), []byte("local\n"), 0o644))

	must(t, Run(context.Background(), []string{"push"}))
	if got := remote.get("main.tex"); got != "local\n" {
		t.Fatalf("remote not updated: %q", got)
	}
	if got := remote.deleteCount(); got != 0 {
		t.Fatalf("existing document was deleted %d time(s) before upload", got)
	}
}

func TestPushDeleteRemovesRemoteFile(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{"main.tex": "base\n"})
	must(t, os.Remove(filepath.Join(project, "main.tex")))

	must(t, Run(context.Background(), []string{"push", "--delete"}))
	if got := remote.get("main.tex"); got != "" {
		t.Fatalf("remote file was not deleted: %q", got)
	}
}

func TestPushDeleteFailsWhenRemoteStillContainsDeletedFile(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{"main.tex": "base\n"})
	remote.ignoreDeletes = true
	must(t, os.Remove(filepath.Join(project, "main.tex")))

	err := Run(context.Background(), []string{"push", "--delete"})
	if err == nil || !strings.Contains(err.Error(), "delete verification failed") {
		t.Fatalf("expected delete verification failure, got %v", err)
	}
	if got := remote.get("main.tex"); got != "base\n" {
		t.Fatalf("remote file should still exist: %q", got)
	}
}

func TestPushDeleteUsesStoredRemoteIDAfterUpload(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{})
	remote.hideEntities = true
	must(t, os.WriteFile(filepath.Join(project, "test.txt"), []byte("created\n"), 0o644))

	must(t, Run(context.Background(), []string{"push"}))
	var state State
	must(t, readJSON(filepath.Join(project, ".overleaf-sync", "state.json"), &state))
	if got := state.Files["test.txt"].RemoteID; got != "doc:doc-test_txt" {
		t.Fatalf("RemoteID = %q", got)
	}

	must(t, os.Remove(filepath.Join(project, "test.txt")))
	must(t, Run(context.Background(), []string{"push", "--delete"}))
	if got := remote.get("test.txt"); got != "" {
		t.Fatalf("remote file was not deleted: %q", got)
	}
}

func TestPushDeleteUsesStoredRemoteIDAfterPull(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{})
	remote.hideEntities = true
	must(t, os.WriteFile(filepath.Join(project, "test.txt"), []byte("created\n"), 0o644))

	must(t, Run(context.Background(), []string{"push"}))
	remote.set("test.txt", "remote\n")
	must(t, Run(context.Background(), []string{"pull"}))
	must(t, os.Remove(filepath.Join(project, "test.txt")))

	must(t, Run(context.Background(), []string{"push", "--delete"}))
	if got := remote.get("test.txt"); got != "" {
		t.Fatalf("remote file was not deleted: %q", got)
	}
}

func TestPushDeleteUsesStoredRemoteDirID(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{})
	remote.hideEntities = true
	must(t, os.Mkdir(filepath.Join(project, "newdir"), 0o755))
	must(t, os.WriteFile(filepath.Join(project, "newdir", "newfile.txt"), []byte("created\n"), 0o644))

	must(t, Run(context.Background(), []string{"push"}))
	var state State
	must(t, readJSON(filepath.Join(project, ".overleaf-sync", "state.json"), &state))
	if got := state.Files["newdir/newfile.txt"].RemoteDir["newdir"]; got != "folder:folder-newdir" {
		t.Fatalf("RemoteDir[newdir] = %q", got)
	}

	must(t, os.RemoveAll(filepath.Join(project, "newdir")))
	must(t, Run(context.Background(), []string{"push", "--delete"}))
	if remote.folderExists("newdir") {
		t.Fatal("remote folder was not deleted")
	}
}

func TestPushDeletedDirsOnlyIncludesEmptyRemoteDirs(t *testing.T) {
	local := map[string]FileSnapshot{
		"keep/one.tex": {Path: "keep/one.tex"},
	}
	remote := map[string]FileSnapshot{
		"chapters/old.tex":        {Path: "chapters/old.tex"},
		"chapters/nested/two.tex": {Path: "chapters/nested/two.tex"},
		"keep/one.tex":            {Path: "keep/one.tex"},
		"keep/deleted.tex":        {Path: "keep/deleted.tex"},
	}

	got := pushDeletedDirs(local, remote, []string{
		"chapters/old.tex",
		"chapters/nested/two.tex",
		"keep/deleted.tex",
	})
	want := []string{"chapters/nested", "chapters"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("deleted dirs = %#v, want %#v", got, want)
	}
}

func TestPullRefusesConflict(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{"main.tex": "base\n"})
	must(t, os.WriteFile(filepath.Join(project, "main.tex"), []byte("local\n"), 0o644))
	remote.set("main.tex", "remote\n")

	err := Run(context.Background(), []string{"pull"})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected pull conflict, got %v", err)
	}
	got := mustRead(t, filepath.Join(project, "main.tex"))
	if got != "local\n" {
		t.Fatalf("local was overwritten: %q", got)
	}
}

func TestPullWithoutAuthAndRemoteDirFails(t *testing.T) {
	project := t.TempDir()
	oldwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	must(t, os.Chdir(project))

	err := Run(context.Background(), []string{"init", "p1"})
	if err == nil || !strings.Contains(err.Error(), "run ovl auth") {
		t.Fatalf("expected auth error, got %v", err)
	}
}

func TestForcePullOverwritesConflictAndDeletesLocal(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{"main.tex": "base\n"})
	must(t, os.WriteFile(filepath.Join(project, "main.tex"), []byte("local\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(project, "local-only.tex"), []byte("draft\n"), 0o644))
	remote.set("main.tex", "remote\n")

	must(t, Run(context.Background(), []string{"pull", "--force"}))
	if got := mustRead(t, filepath.Join(project, "main.tex")); got != "remote\n" {
		t.Fatalf("local file not overwritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(project, "local-only.tex")); !os.IsNotExist(err) {
		t.Fatalf("local-only file should be deleted, err=%v", err)
	}
	backups, err := filepath.Glob(filepath.Join(project, ".overleaf-sync", "backups", "local-*"))
	must(t, err)
	if len(backups) != 1 {
		t.Fatalf("expected one local backup, got %d", len(backups))
	}
	if got := mustRead(t, filepath.Join(backups[0], "main.tex")); got != "local\n" {
		t.Fatalf("backup did not contain previous local main.tex: %q", got)
	}
	if got := mustRead(t, filepath.Join(backups[0], "local-only.tex")); got != "draft\n" {
		t.Fatalf("backup did not contain deleted local-only file: %q", got)
	}
}

func TestForcePushDoesNotRequireYes(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{"main.tex": "base\n"})
	must(t, os.WriteFile(filepath.Join(project, "main.tex"), []byte("local\n"), 0o644))
	remote.set("main.tex", "remote\n")

	must(t, Run(context.Background(), []string{"push", "--force"}))
	if got := remote.get("main.tex"); got != "local\n" {
		t.Fatalf("remote file not force overwritten: %q", got)
	}
	backups, err := filepath.Glob(filepath.Join(project, ".overleaf-sync", "backups", "remote-*"))
	must(t, err)
	if len(backups) != 1 {
		t.Fatalf("expected one remote backup, got %d", len(backups))
	}
	if got := mustRead(t, filepath.Join(backups[0], "main.tex")); got != "remote\n" {
		t.Fatalf("backup did not contain previous remote main.tex: %q", got)
	}
}

func TestForcePushDeletesRemoteOnlyFiles(t *testing.T) {
	_, remote := setupSyncedProject(t, map[string]string{"main.tex": "base\n"})
	remote.set("remote-only.tex", "remote\n")

	must(t, Run(context.Background(), []string{"push", "--force"}))
	if got := remote.get("remote-only.tex"); got != "" {
		t.Fatalf("remote-only file was not deleted: %q", got)
	}
}

func TestForcePushDeletesLocallyDeletedFiles(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{"main.tex": "base\n", "old.tex": "old\n"})
	must(t, os.Remove(filepath.Join(project, "old.tex")))

	must(t, Run(context.Background(), []string{"push", "--force"}))
	if got := remote.get("old.tex"); got != "" {
		t.Fatalf("locally deleted file was not deleted remotely: %q", got)
	}
}

func TestCompileDownloadsPDF(t *testing.T) {
	project, _ := setupSyncedProject(t, map[string]string{"main.tex": "\\documentclass{article}\\begin{document}ok\\end{document}\n"})

	must(t, Run(context.Background(), []string{"compile", "main.tex"}))
	if got := mustRead(t, filepath.Join(project, "main.pdf")); got != "%PDF fake\n" {
		t.Fatalf("unexpected pdf content: %q", got)
	}
}

func TestCompilePushUploadsBeforeCompile(t *testing.T) {
	project, remote := setupSyncedProject(t, map[string]string{"main.tex": "base\n"})
	must(t, os.WriteFile(filepath.Join(project, "main.tex"), []byte("local\n"), 0o644))

	must(t, Run(context.Background(), []string{"compile", "--push", "main.tex"}))
	if got := remote.get("main.tex"); got != "local\n" {
		t.Fatalf("compile --push did not upload local change: %q", got)
	}
	if got := mustRead(t, filepath.Join(project, "main.pdf")); got != "%PDF fake\n" {
		t.Fatalf("unexpected pdf content: %q", got)
	}
}

func TestOlignoreExcludesFiles(t *testing.T) {
	project := t.TempDir()
	must(t, os.WriteFile(filepath.Join(project, ".olignore"), []byte("*.log\nbuild/\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(project, "main.tex"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(project, "debug.log"), []byte("x"), 0o644))
	must(t, os.Mkdir(filepath.Join(project, "build"), 0o755))
	must(t, os.WriteFile(filepath.Join(project, "build", "out.txt"), []byte("x"), 0o644))

	got, err := scanLocal(project, Config{IgnoreFile: ".olignore"})
	must(t, err)
	if _, ok := got["main.tex"]; !ok {
		t.Fatal("main.tex missing")
	}
	if _, ok := got["debug.log"]; ok {
		t.Fatal("debug.log should be ignored")
	}
	if _, ok := got["build/out.txt"]; ok {
		t.Fatal("build/out.txt should be ignored")
	}
}

func TestInitCreatesOrAppendsGitignore(t *testing.T) {
	project := t.TempDir()
	must(t, ensureGitIgnore(project))
	if got := mustRead(t, filepath.Join(project, ".gitignore")); got != ".overleaf-sync/\n" {
		t.Fatalf("unexpected .gitignore: %q", got)
	}

	project2 := t.TempDir()
	must(t, os.WriteFile(filepath.Join(project2, ".gitignore"), []byte("dist/\n"), 0o644))
	must(t, ensureGitIgnore(project2))
	if got := mustRead(t, filepath.Join(project2, ".gitignore")); got != "dist/\n.overleaf-sync/\n" {
		t.Fatalf("unexpected appended .gitignore: %q", got)
	}
}

func TestAuthStoresProjectCookieByDefault(t *testing.T) {
	project := t.TempDir()
	oldwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	must(t, os.Chdir(project))

	oldInput := authInput
	authInput = strings.NewReader("overleaf_session2=local-cookie\n")
	t.Cleanup(func() { authInput = oldInput })
	must(t, Run(context.Background(), []string{"auth"}))

	cookiePath := filepath.Join(project, ".overleaf-sync", "auth", "cookie")
	if got := mustRead(t, cookiePath); got != "overleaf_session2=local-cookie\n" {
		t.Fatalf("unexpected cookie file: %q", got)
	}
	var cfg Config
	must(t, readJSON(filepath.Join(project, ".overleaf-sync", "config.json"), &cfg))
	if cfg.AuthFile != ".overleaf-sync/auth/cookie" {
		t.Fatalf("AuthFile = %q", cfg.AuthFile)
	}
	cookie, err := loadCookie(project, cfg)
	must(t, err)
	if cookie != "overleaf_session2=local-cookie" {
		t.Fatalf("cookie = %q", cookie)
	}
}

func TestAuthFileStoresProjectCookieReference(t *testing.T) {
	project := t.TempDir()
	oldwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	must(t, os.Chdir(project))

	oldInput := authInput
	authInput = strings.NewReader("file-cookie\n")
	t.Cleanup(func() { authInput = oldInput })
	must(t, Run(context.Background(), []string{"auth", "--file", "secrets/overleaf-cookie"}))

	if got := mustRead(t, filepath.Join(project, "secrets", "overleaf-cookie")); got != "file-cookie\n" {
		t.Fatalf("unexpected cookie file: %q", got)
	}
	var cfg Config
	must(t, readJSON(filepath.Join(project, ".overleaf-sync", "config.json"), &cfg))
	if cfg.AuthFile != "secrets/overleaf-cookie" {
		t.Fatalf("AuthFile = %q", cfg.AuthFile)
	}
}

func TestLoadCookieEnvOverridesProjectCookie(t *testing.T) {
	project := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(project, ".overleaf-sync", "auth"), 0o755))
	must(t, os.WriteFile(filepath.Join(project, ".overleaf-sync", "auth", "cookie"), []byte("project-cookie\n"), 0o600))
	t.Setenv("OVL_COOKIE", "env-cookie")

	cookie, err := loadCookie(project, Config{AuthFile: ".overleaf-sync/auth/cookie"})
	must(t, err)
	if cookie != "env-cookie" {
		t.Fatalf("cookie = %q", cookie)
	}
}

type fakeOverleaf struct {
	t             *testing.T
	server        *httptest.Server
	mu            sync.Mutex
	files         map[string]string
	folders       map[string]bool
	ignoreDeletes bool
	hideEntities  bool
	deleteCalls   int
}

func setupSyncedProject(t *testing.T, files map[string]string) (string, *fakeOverleaf) {
	t.Helper()
	project := t.TempDir()
	remote := newFakeOverleaf(t, files)
	oldwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	must(t, os.Chdir(project))
	oldInput := authInput
	authInput = strings.NewReader("overleaf_session2=test-cookie\n")
	t.Cleanup(func() { authInput = oldInput })
	must(t, Run(context.Background(), []string{"auth"}))
	must(t, Run(context.Background(), []string{"init", "--host", remote.server.URL, "p1"}))
	must(t, Run(context.Background(), []string{"pull"}))
	return project, remote
}

func newFakeOverleaf(t *testing.T, files map[string]string) *fakeOverleaf {
	t.Helper()
	remote := &fakeOverleaf{t: t, files: map[string]string{}, folders: map[string]bool{}}
	for k, v := range files {
		remote.files[k] = v
	}
	remote.server = httptest.NewServer(http.HandlerFunc(remote.handle))
	t.Cleanup(remote.server.Close)
	return remote
}

func (f *fakeOverleaf) set(path, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = content
	for dir := parentDir(path); dir != ""; dir = parentDir(dir) {
		f.folders[dir] = true
	}
}

func (f *fakeOverleaf) get(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.files[path]
}

func (f *fakeOverleaf) folderExists(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.folders[path]
}

func (f *fakeOverleaf) deleteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deleteCalls
}

func (f *fakeOverleaf) createFolder(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folders[path] = true
}

func (f *fakeOverleaf) folderPath(parentID, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if parentID == "root-id" {
		return name
	}
	for dir := range f.folders {
		if fakeFolderID(dir) == parentID {
			return dir + "/" + name
		}
	}
	return name
}

func (f *fakeOverleaf) filePath(folderID, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if folderID == "" || folderID == "root-id" {
		return name
	}
	for dir := range f.folders {
		if fakeFolderID(dir) == folderID {
			return dir + "/" + name
		}
	}
	return name
}

func (f *fakeOverleaf) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Cookie") == "" {
		http.Error(w, "missing cookie", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/project":
		_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token">`)
	case r.Method == http.MethodGet && r.URL.Path == "/project/p1/download/zip":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(f.zip())
	case r.Method == http.MethodGet && r.URL.Path == "/project/p1":
		projectMeta, err := json.Marshal(f.projectInfo())
		must(f.t, err)
		_, _ = io.WriteString(w, `<meta name="ol-csrfToken" content="csrf-token"><meta name="ol-project" content="`+html.EscapeString(string(projectMeta))+`">`)
	case r.Method == http.MethodGet && r.URL.Path == "/project/p1/entities":
		_ = json.NewEncoder(w).Encode(map[string]any{"entities": f.entities()})
	case r.Method == http.MethodPost && r.URL.Path == "/project/p1/compile":
		if r.Header.Get("X-Csrf-Token") != "csrf-token" {
			f.t.Fatalf("compile csrf = %q", r.Header.Get("X-Csrf-Token"))
		}
		var payload struct {
			RootDocID        string `json:"rootDoc_id"`
			RootResourcePath string `json:"rootResourcePath"`
		}
		must(f.t, json.NewDecoder(r.Body).Decode(&payload))
		if payload.RootResourcePath != "main.tex" {
			f.t.Fatalf("rootResourcePath = %q", payload.RootResourcePath)
		}
		if payload.RootDocID != fakeID("main.tex") {
			f.t.Fatalf("rootDoc_id = %q", payload.RootDocID)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":       "success",
			"clsiServerId": "clsi-1",
			"outputFiles": []map[string]string{
				{"path": "output.pdf", "url": "/project/p1/build/build-1/output/output.pdf", "type": "pdf"},
			},
		})
	case r.Method == http.MethodGet && r.URL.Path == "/project/p1/build/build-1/output/output.pdf":
		if r.URL.Query().Get("clsiserverid") != "clsi-1" {
			f.t.Fatalf("clsiserverid = %q", r.URL.Query().Get("clsiserverid"))
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = io.WriteString(w, "%PDF fake\n")
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/project/p1/doc/"):
		id := strings.TrimPrefix(r.URL.Path, "/project/p1/doc/")
		f.deleteByID(id)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/project/p1/folder/"):
		id := strings.TrimPrefix(r.URL.Path, "/project/p1/folder/")
		f.deleteFolderByID(id)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/project/p1/folder":
		var payload struct {
			ParentFolderID string `json:"parent_folder_id"`
			Name           string `json:"name"`
		}
		must(f.t, json.NewDecoder(r.Body).Decode(&payload))
		path := f.folderPath(payload.ParentFolderID, payload.Name)
		f.createFolder(path)
		_ = json.NewEncoder(w).Encode(map[string]string{"_id": fakeFolderID(path), "name": payload.Name})
	case r.Method == http.MethodPost && r.URL.Path == "/project/p1/upload":
		must(f.t, r.ParseMultipartForm(1<<20))
		name := r.FormValue("name")
		file, _, err := r.FormFile("qqfile")
		must(f.t, err)
		defer file.Close()
		body, err := io.ReadAll(file)
		must(f.t, err)
		remotePath := f.filePath(r.URL.Query().Get("folder_id"), name)
		f.set(remotePath, string(body))
		_, _ = io.WriteString(w, `{"success":true,"entity_id":"`+fakeID(remotePath)+`","entity_type":"doc"}`)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeOverleaf) zip() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return zipBytes(f.t, f.files)
}

func (f *fakeOverleaf) projectInfo() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hideEntities {
		return map[string]any{
			"rootFolder": []map[string]any{
				{"_id": "root-id", "name": "root", "docs": []any{}, "fileRefs": []any{}, "folders": f.projectFolders("")},
			},
		}
	}
	var docs []map[string]string
	for path := range f.files {
		docs = append(docs, map[string]string{"_id": fakeID(path), "name": path})
	}
	return map[string]any{
		"rootFolder": []map[string]any{
			{"_id": "root-id", "name": "root", "docs": docs, "fileRefs": []any{}, "folders": f.projectFolders("")},
		},
	}
}

func (f *fakeOverleaf) projectFolders(prefix string) []map[string]any {
	children := map[string]bool{}
	for dir := range f.folders {
		parent := parentDir(dir)
		if parent == prefix {
			name := strings.TrimPrefix(dir, prefix)
			name = strings.TrimPrefix(name, "/")
			children[name] = true
		}
	}
	var out []map[string]any
	for name := range children {
		path := name
		if prefix != "" {
			path = prefix + "/" + name
		}
		out = append(out, map[string]any{"_id": fakeFolderID(path), "name": name, "docs": []any{}, "fileRefs": []any{}, "folders": f.projectFolders(path)})
	}
	return out
}

func (f *fakeOverleaf) entities() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hideEntities {
		return nil
	}
	var out []map[string]string
	for path := range f.files {
		out = append(out, map[string]string{"_id": fakeID(path), "path": path, "type": "doc", "name": path})
	}
	return out
}

func (f *fakeOverleaf) deleteByID(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	if f.ignoreDeletes {
		return
	}
	for path := range f.files {
		if fakeID(path) == id {
			delete(f.files, path)
			return
		}
	}
}

func (f *fakeOverleaf) deleteFolderByID(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for dir := range f.folders {
		if fakeFolderID(dir) == id {
			delete(f.folders, dir)
			for path := range f.files {
				if strings.HasPrefix(path, dir+"/") {
					delete(f.files, path)
				}
			}
			for child := range f.folders {
				if strings.HasPrefix(child, dir+"/") {
					delete(f.folders, child)
				}
			}
			return
		}
	}
}

func fakeID(path string) string {
	return "doc-" + strings.NewReplacer("/", "_", ".", "_").Replace(path)
}

func fakeFolderID(path string) string {
	return "folder-" + strings.NewReplacer("/", "_", ".", "_").Replace(path)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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
