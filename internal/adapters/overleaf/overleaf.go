package overleaf

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ovl/internal/domain"
)

type overleafBackend struct {
	host      string
	projectID string
	cookie    string
	csrf      string
	client    *http.Client
}

func New(host, projectID, cookie string, client *http.Client) *overleafBackend {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if client.Jar == nil {
		jar, _ := cookiejar.New(nil)
		client.Jar = jar
	}
	return &overleafBackend{
		host:      host,
		projectID: projectID,
		cookie:    cookie,
		client:    client,
	}
}

type remoteEntity struct {
	ID       string
	Type     string
	FolderID string
}

type entityLookup struct {
	Entities  map[string]remoteEntity
	Source    string
	TreeErr   error
	SocketErr error
	APIErr    error
}

type entityResponse struct {
	Entities []entityItem `json:"entities"`
}

type entityItem struct {
	ID         string `json:"_id"`
	ID2        string `json:"id"`
	DocID      string `json:"doc_id"`
	FileID     string `json:"file_id"`
	Path       string `json:"path"`
	Pathname   string `json:"pathname"`
	FilePath   string `json:"filePath"`
	Type       string `json:"type"`
	EntityType string `json:"entityType"`
	Name       string `json:"name"`
}

type projectFolder struct {
	ID      string          `json:"_id"`
	Name    string          `json:"name"`
	Docs    []projectEntity `json:"docs"`
	Files   []projectEntity `json:"fileRefs"`
	Folders []projectFolder `json:"folders"`
}

type projectEntity struct {
	ID   string `json:"_id"`
	ID2  string `json:"id"`
	Name string `json:"name"`
}

type projectInfo struct {
	RootFolder []projectFolder `json:"rootFolder"`
}

func (p *projectInfo) UnmarshalJSON(data []byte) error {
	var raw struct {
		RootFolder json.RawMessage `json:"rootFolder"`
		Project    *struct {
			RootFolder json.RawMessage `json:"rootFolder"`
		} `json:"project"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	root := raw.RootFolder
	if len(root) == 0 && raw.Project != nil {
		root = raw.Project.RootFolder
	}
	if len(root) == 0 || string(root) == "null" {
		return nil
	}
	var folders []projectFolder
	if err := json.Unmarshal(root, &folders); err == nil {
		p.RootFolder = folders
		return nil
	}
	var folder projectFolder
	if err := json.Unmarshal(root, &folder); err != nil {
		return err
	}
	p.RootFolder = []projectFolder{folder}
	return nil
}

func (b *overleafBackend) List(ctx context.Context) (map[string]domain.FileSnapshot, error) {
	if b.projectID == "" {
		return nil, errors.New("missing project id; run ovl init <project-id>")
	}
	req, err := b.newRequest(ctx, http.MethodGet, fmt.Sprintf("/project/%s/download/zip", b.projectID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/zip,*/*")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download remote zip failed: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return snapshotsFromZip(body)
}

func (b *overleafBackend) Put(ctx context.Context, snap domain.FileSnapshot) (domain.FileState, error) {
	info, lookup, err := b.projectInfo(ctx)
	if err != nil {
		return domain.FileState{}, err
	}
	// Overleaf's upload endpoint upserts an entity with the same folder and name.
	// In particular, it updates an existing doc in place and records an OT diff;
	// deleting it first would sever its history and review context.
	existing, hadExisting := lookup.Entities[snap.Path]
	folderID, remoteDirs, err := b.ensureFolder(ctx, info, snap.Path)
	if err != nil {
		return domain.FileState{}, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	base := pathBase(snap.Path)
	if err := writer.WriteField("targetFolderId", folderID); err != nil {
		return domain.FileState{}, err
	}
	if err := writer.WriteField("name", base); err != nil {
		return domain.FileState{}, err
	}
	if err := writer.WriteField("type", mimeType(base)); err != nil {
		return domain.FileState{}, err
	}
	part, err := writer.CreateFormFile("qqfile", base)
	if err != nil {
		return domain.FileState{}, err
	}
	if _, err := part.Write(snap.Content); err != nil {
		return domain.FileState{}, err
	}
	if err := writer.Close(); err != nil {
		return domain.FileState{}, err
	}
	req, err := b.newRequest(ctx, http.MethodPost, fmt.Sprintf("/project/%s/upload?folder_id=%s", b.projectID, url.QueryEscape(folderID)), &body)
	if err != nil {
		return domain.FileState{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Csrf-Token", b.csrf)
	resp, err := b.client.Do(req)
	if err != nil {
		return domain.FileState{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domain.FileState{}, fmt.Errorf("upload %s failed: HTTP %d: %s", snap.Path, resp.StatusCode, trimBody(respBody))
	}
	var result struct {
		Success    bool   `json:"success"`
		EntityID   string `json:"entity_id"`
		EntityType string `json:"entity_type"`
	}
	if len(respBody) > 0 && json.Unmarshal(respBody, &result) == nil && !result.Success {
		return domain.FileState{}, fmt.Errorf("upload %s failed: %s", snap.Path, trimBody(respBody))
	}
	remoteStateID := remoteID(result.EntityType, result.EntityID)
	if remoteStateID == "" && hadExisting {
		remoteStateID = remoteID(existing.Type, existing.ID)
	}
	return domain.FileState{
		RemoteID:  remoteStateID,
		RemoteDir: remoteDirs,
		Type:      snap.Type,
		SHA256:    snap.SHA256,
		Size:      snap.Size,
	}, nil
}

func (b *overleafBackend) Delete(ctx context.Context, rel string, hint domain.FileState) error {
	isDir := strings.HasSuffix(rel, "/")
	if entity, ok := remoteEntityFromState(hint); ok {
		if b.csrf == "" {
			if err := b.fetchCSRF(ctx); err != nil {
				return err
			}
		}
		return b.deleteEntity(ctx, entity.ID, entity.Type)
	}
	_, lookup, err := b.projectInfo(ctx)
	if err != nil {
		return err
	}
	rel = strings.TrimSuffix(rel, "/")
	entity, ok := lookup.Entities[rel]
	if !ok {
		if isDir {
			return nil
		}
		return fmt.Errorf("remote entity %s not found (%s)", rel, lookup.summary())
	}
	return b.deleteEntity(ctx, entity.ID, entity.Type)
}

func (b *overleafBackend) CompilePDF(ctx context.Context, rootPath string) (domain.CompileResult, error) {
	if b.projectID == "" {
		return domain.CompileResult{}, errors.New("missing project id; run ovl init <project-id>")
	}
	rootPath = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(rootPath)), "/")
	if b.csrf == "" {
		if err := b.fetchCSRF(ctx); err != nil {
			return domain.CompileResult{}, err
		}
	}

	var rootDocID string
	if rootPath != "" {
		_, lookup, err := b.projectInfo(ctx)
		if err != nil {
			return domain.CompileResult{}, err
		}
		entity, ok := lookup.Entities[rootPath]
		if !ok {
			return domain.CompileResult{}, fmt.Errorf("root document %s not found on remote (%s)", rootPath, lookup.summary())
		}
		if entity.Type != "doc" {
			return domain.CompileResult{}, fmt.Errorf("root document %s is a %s, not an Overleaf doc", rootPath, entity.Type)
		}
		rootDocID = entity.ID
	}
	if rootPath == "" {
		rootPath = "main.tex"
	}

	editorID := randomUUID()
	payload := map[string]any{
		"rootResourcePath":           rootPath,
		"draft":                      false,
		"check":                      "silent",
		"incrementalCompilesEnabled": true,
		"stopOnFirstError":           false,
		"editorId":                   editorID,
	}
	if rootDocID != "" {
		payload["rootDoc_id"] = rootDocID
	}
	body, _ := json.Marshal(payload)
	req, err := b.newRequest(ctx, http.MethodPost, fmt.Sprintf("/project/%s/compile", b.projectID), bytes.NewReader(body))
	if err != nil {
		return domain.CompileResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Csrf-Token", b.csrf)
	resp, err := b.client.Do(req)
	if err != nil {
		return domain.CompileResult{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.CompileResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domain.CompileResult{}, fmt.Errorf("compile failed: HTTP %d: %s", resp.StatusCode, trimBody(respBody))
	}
	result, err := parseCompileResponse(respBody)
	if err != nil {
		return domain.CompileResult{}, err
	}
	if result.Status != "success" {
		return domain.CompileResult{}, fmt.Errorf("compile status is %s", result.Status)
	}
	pdf := result.outputPDF()
	if pdf.Path == "" {
		return domain.CompileResult{}, errors.New("compile succeeded but output.pdf was not returned")
	}
	pdfBody, err := b.downloadCompileOutput(ctx, pdf.URL, result.CLSIServerID, editorID)
	if err != nil {
		return domain.CompileResult{}, err
	}
	return domain.CompileResult{PDF: pdfBody, Status: result.Status}, nil
}

type compileResponse struct {
	Status       string              `json:"status"`
	OutputFiles  []compileOutputFile `json:"outputFiles"`
	CLSIServerID string              `json:"clsiServerId"`
}

type compileOutputFile struct {
	Path string `json:"path"`
	URL  string `json:"url"`
	Type string `json:"type"`
}

func parseCompileResponse(body []byte) (compileResponse, error) {
	var result compileResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return compileResponse{}, fmt.Errorf("parse compile response failed: %w", err)
	}
	return result, nil
}

func (r compileResponse) outputPDF() compileOutputFile {
	for _, file := range r.OutputFiles {
		if file.Path == "output.pdf" || file.Type == "pdf" {
			return file
		}
	}
	return compileOutputFile{}
}

func (b *overleafBackend) downloadCompileOutput(ctx context.Context, outputURL, clsiServerID, editorID string) ([]byte, error) {
	if outputURL == "" {
		return nil, errors.New("output.pdf URL is missing from compile response")
	}
	req, err := b.newRequest(ctx, http.MethodGet, compileDownloadPath(outputURL, clsiServerID, editorID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/pdf,*/*")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download compiled PDF failed: HTTP %d: %s", resp.StatusCode, trimBody(body))
	}
	return body, nil
}

func compileDownloadPath(outputURL, clsiServerID, editorID string) string {
	parsed, err := url.Parse(outputURL)
	if err != nil {
		return outputURL
	}
	if parsed.IsAbs() {
		outputURL = parsed.RequestURI()
		parsed, _ = url.Parse(outputURL)
	}
	query := parsed.Query()
	if clsiServerID != "" && query.Get("clsiserverid") == "" {
		query.Set("clsiserverid", clsiServerID)
	}
	if editorID != "" && query.Get("editorId") == "" {
		query.Set("editorId", editorID)
	}
	parsed.RawQuery = query.Encode()
	return parsed.RequestURI()
}

func (b *overleafBackend) projectInfo(ctx context.Context) (projectInfo, entityLookup, error) {
	if b.csrf == "" {
		if err := b.fetchCSRF(ctx); err != nil {
			return projectInfo{}, entityLookup{}, err
		}
	}
	req, err := b.newRequest(ctx, http.MethodGet, fmt.Sprintf("/project/%s", b.projectID), nil)
	if err != nil {
		return projectInfo{}, entityLookup{}, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return projectInfo{}, entityLookup{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return projectInfo{}, entityLookup{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return projectInfo{}, entityLookup{}, fmt.Errorf("fetch project page failed: HTTP %d", resp.StatusCode)
	}
	if csrf := extractCSRF(body); csrf != "" {
		b.csrf = csrf
	}
	lookup := entityLookup{Entities: map[string]remoteEntity{}}
	info, err := extractProjectInfo(body)
	lookup.TreeErr = err
	if err != nil {
		socketInfo, socketErr := b.projectInfoFromSocket(ctx, extractWSURL(body))
		lookup.SocketErr = socketErr
		if socketErr == nil && len(socketInfo.RootFolder) > 0 {
			info = socketInfo
			lookup.Source = "socket"
		} else {
			rootID := computeRootFolderID(b.projectID)
			if rootID == "" {
				return projectInfo{}, entityLookup{}, fmt.Errorf("%w; socket fallback failed: %v", err, socketErr)
			}
			info = projectInfo{RootFolder: []projectFolder{{ID: rootID, Name: "root"}}}
			lookup.Source = "computed-root"
		}
	} else {
		lookup.Source = "project-page"
	}
	if len(info.RootFolder) > 0 {
		walkRemoteFolder(info.RootFolder[0], "", lookup.Entities)
	}
	apiEntities, apiErr := b.entities(ctx)
	lookup.APIErr = apiErr
	if apiErr == nil {
		for path, entity := range apiEntities {
			if _, exists := lookup.Entities[path]; !exists {
				lookup.Entities[path] = entity
			}
		}
	}
	return info, lookup, nil
}

func (b *overleafBackend) entities(ctx context.Context) (map[string]remoteEntity, error) {
	req, err := b.newRequest(ctx, http.MethodGet, fmt.Sprintf("/project/%s/entities", b.projectID), nil)
	if err != nil {
		return nil, err
	}
	if b.csrf != "" {
		req.Header.Set("X-Csrf-Token", b.csrf)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("entities failed: HTTP %d", resp.StatusCode)
	}
	var wrapped entityResponse
	if err := json.Unmarshal(body, &wrapped); err != nil || wrapped.Entities == nil {
		var direct []entityItem
		if directErr := json.Unmarshal(body, &direct); directErr != nil {
			if err != nil {
				return nil, err
			}
			return nil, directErr
		}
		wrapped.Entities = direct
	}
	out := map[string]remoteEntity{}
	for _, item := range wrapped.Entities {
		path := strings.TrimPrefix(filepath.ToSlash(firstNonEmpty(item.Path, item.Pathname, item.FilePath)), "/")
		if path == "" {
			path = item.Name
		}
		id := firstNonEmpty(item.ID, item.ID2, item.DocID, item.FileID)
		typ := normalizeEntityType(firstNonEmpty(item.Type, item.EntityType))
		if typ == "" {
			if item.DocID != "" {
				typ = "doc"
			}
			if item.FileID != "" {
				typ = "file"
			}
		}
		if path != "" && id != "" && typ != "" {
			out[path] = remoteEntity{ID: id, Type: typ}
		}
	}
	return out, nil
}

func (b *overleafBackend) projectInfoFromSocket(ctx context.Context, wsURL string) (projectInfo, error) {
	socketBase, socketResource := b.socketEndpoint(wsURL)
	engineInfo, engineErr := b.projectInfoFromEngineIO(ctx, socketBase, socketResource)
	if engineErr == nil {
		return engineInfo, nil
	}
	info, err := b.projectInfoFromLegacySocketIO(ctx, socketBase, socketResource)
	if err == nil {
		return info, nil
	}
	return projectInfo{}, fmt.Errorf("engine.io fallback failed: %v; legacy socket.io fallback failed: %v", engineErr, err)
}

func (b *overleafBackend) projectInfoFromLegacySocketIO(ctx context.Context, socketBase, socketResource string) (projectInfo, error) {
	sid, err := b.legacySocketHandshake(ctx, socketBase, socketResource)
	if err != nil {
		return projectInfo{}, err
	}
	wsInfo, wsErr := b.projectInfoFromLegacyWebSocket(ctx, socketBase, socketResource, sid)
	if wsErr == nil {
		return wsInfo, nil
	}
	var fallbackErr error
	defer func() {
		_ = b.socketPost(context.Background(), socketBase, socketResource, sid, "0::")
	}()
	pollPaths := []func() string{
		func() string {
			return fmt.Sprintf("/%s/1/xhr-polling/%s?projectId=%s&t=%d", socketResource, url.PathEscape(sid), url.QueryEscape(b.projectID), time.Now().UnixMilli())
		},
		func() string {
			return fmt.Sprintf("/%s/1/xhr-polling/%s?t=%d", socketResource, url.PathEscape(sid), time.Now().UnixMilli())
		},
	}
	var lastErr error
	var previews []string
	for _, pollPath := range pollPaths {
		for attempt := 0; attempt < 12; attempt++ {
			req, err := b.newSocketRequest(ctx, http.MethodGet, socketBase, pollPath(), nil)
			if err != nil {
				return projectInfo{}, err
			}
			resp, err := b.client.Do(req)
			if err != nil {
				return projectInfo{}, err
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return projectInfo{}, err
			}
			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("socket poll failed: HTTP %d", resp.StatusCode)
				break
			}
			if len(previews) < 3 {
				previews = append(previews, bodyPreview(body))
			}
			for _, packet := range decodeSocketPayload(string(body)) {
				switch {
				case strings.HasPrefix(packet, "1::"):
					continue
				case strings.HasPrefix(packet, "7:::"):
					time.Sleep(150 * time.Millisecond)
					continue
				case strings.HasPrefix(packet, "2::"):
					_ = b.legacySocketPostVariants(ctx, socketBase, socketResource, sid, "2::")
					continue
				}
				if info, ok := projectInfoFromSocketIOPacket(packet); ok {
					return info, nil
				}
			}
		}
	}
	if lastErr != nil {
		fallbackErr = fmt.Errorf("xhr-polling joinProjectResponse not found: %w; polls=%s", lastErr, strings.Join(previews, " | "))
		return projectInfo{}, fmt.Errorf("websocket fallback failed: %v; %w", wsErr, fallbackErr)
	}
	if len(previews) > 0 {
		fallbackErr = fmt.Errorf("xhr-polling joinProjectResponse not found: polls=%s", strings.Join(previews, " | "))
		return projectInfo{}, fmt.Errorf("websocket fallback failed: %v; %w", wsErr, fallbackErr)
	}
	return projectInfo{}, fmt.Errorf("websocket fallback failed: %v; xhr-polling joinProjectResponse not found", wsErr)
}

func (b *overleafBackend) legacySocketHandshake(ctx context.Context, socketBase, socketResource string) (string, error) {
	paths := []string{
		fmt.Sprintf("/%s/1/?projectId=%s&t=%d", socketResource, url.QueryEscape(b.projectID), time.Now().UnixMilli()),
		fmt.Sprintf("/%s/1/?t=%d&projectId=%s", socketResource, time.Now().UnixMilli(), url.QueryEscape(b.projectID)),
		fmt.Sprintf("/%s/1/?t=%d", socketResource, time.Now().UnixMilli()),
	}
	var previews []string
	for _, path := range paths {
		req, err := b.newSocketRequest(ctx, http.MethodGet, socketBase, path, nil)
		if err != nil {
			return "", err
		}
		resp, err := b.client.Do(req)
		if err != nil {
			return "", err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", err
		}
		previews = append(previews, bodyPreview(body))
		if resp.StatusCode != http.StatusOK {
			continue
		}
		sid := strings.SplitN(strings.TrimSpace(string(body)), ":", 2)[0]
		if sid != "" && !strings.Contains(sid, " ") && !strings.Contains(sid, "<") {
			return sid, nil
		}
	}
	return "", fmt.Errorf("socket handshake returned no session id: %s", strings.Join(previews, " | "))
}

func (b *overleafBackend) projectInfoFromLegacyWebSocket(ctx context.Context, socketBase, socketResource, sid string) (projectInfo, error) {
	u, err := url.Parse(socketBase)
	if err != nil {
		return projectInfo{}, err
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	var conn net.Conn
	dialer := net.Dialer{}
	if u.Scheme == "https" {
		conn, err = tls.DialWithDialer(&dialer, "tcp", host, &tls.Config{ServerName: u.Hostname()})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", host)
	}
	if err != nil {
		return projectInfo{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return projectInfo{}, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := fmt.Sprintf("/%s/1/websocket/%s?projectId=%s", socketResource, url.PathEscape(sid), url.QueryEscape(b.projectID))
	origin := b.host
	if parsedOrigin, err := url.Parse(origin); err == nil {
		origin = parsedOrigin.Scheme + "://" + parsedOrigin.Host
	}
	req := strings.Builder{}
	req.WriteString("GET " + path + " HTTP/1.1\r\n")
	req.WriteString("Host: " + u.Host + "\r\n")
	req.WriteString("Upgrade: websocket\r\n")
	req.WriteString("Connection: Upgrade\r\n")
	req.WriteString("Sec-WebSocket-Key: " + key + "\r\n")
	req.WriteString("Sec-WebSocket-Version: 13\r\n")
	req.WriteString("Origin: " + origin + "\r\n")
	req.WriteString("Cookie: " + b.socketCookieHeader(u) + "\r\n")
	req.WriteString("User-Agent: ovl/0.1\r\n")
	req.WriteString("\r\n")
	if _, err := io.WriteString(conn, req.String()); err != nil {
		return projectInfo{}, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		return projectInfo{}, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return projectInfo{}, fmt.Errorf("websocket upgrade failed: HTTP %d", resp.StatusCode)
	}
	for attempt := 0; attempt < 20; attempt++ {
		opcode, payload, err := readWebSocketFrame(reader)
		if err != nil {
			return projectInfo{}, err
		}
		switch opcode {
		case 0x1:
			packet := string(payload)
			if packet == "2::" || packet == "2" {
				_ = writeWebSocketText(conn, packet)
				continue
			}
			if info, ok := projectInfoFromSocketIOPacket(packet); ok {
				return info, nil
			}
		case 0x8:
			return projectInfo{}, errors.New("websocket closed before joinProjectResponse")
		case 0x9:
			_ = writeWebSocketFrame(conn, 0xA, payload)
		}
	}
	return projectInfo{}, errors.New("websocket joinProjectResponse not found")
}

func (b *overleafBackend) legacyJoinProjectPacket() string {
	payload := map[string]any{
		"name": "joinProject",
		"args": []map[string]string{{"project_id": b.projectID}},
	}
	body, _ := json.Marshal(payload)
	return "5:::" + string(body)
}

func (b *overleafBackend) legacySocketPostVariants(ctx context.Context, socketBase, socketResource, sid, packet string) error {
	var firstErr error
	payloads := []string{packet, encodeLegacySocketPayload(packet)}
	for _, withProjectID := range []bool{true, false} {
		for _, payload := range payloads {
			if err := b.socketPostWithProjectID(ctx, socketBase, socketResource, sid, payload, withProjectID); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func encodeLegacySocketPayload(packet string) string {
	return "\ufffd" + strconv.Itoa(len(packet)) + "\ufffd" + packet
}

func (b *overleafBackend) projectInfoFromEngineIO(ctx context.Context, socketBase, socketResource string) (projectInfo, error) {
	var errs []string
	for _, eio := range []string{"4", "3"} {
		info, err := b.projectInfoFromEngineIOVersion(ctx, socketBase, socketResource, eio)
		if err == nil {
			return info, nil
		}
		errs = append(errs, "EIO="+eio+": "+err.Error())
	}
	return projectInfo{}, errors.New(strings.Join(errs, "; "))
}

func (b *overleafBackend) projectInfoFromEngineIOVersion(ctx context.Context, socketBase, socketResource, eio string) (projectInfo, error) {
	query := fmt.Sprintf("EIO=%s&transport=polling&projectId=%s&t=%d", url.QueryEscape(eio), url.QueryEscape(b.projectID), time.Now().UnixMilli())
	handshakePath := fmt.Sprintf("/%s/?%s", socketResource, query)
	req, err := b.newSocketRequest(ctx, http.MethodGet, socketBase, handshakePath, nil)
	if err != nil {
		return projectInfo{}, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return projectInfo{}, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return projectInfo{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return projectInfo{}, fmt.Errorf("handshake failed: HTTP %d", resp.StatusCode)
	}
	sid := engineIOSessionID(string(body))
	if sid == "" {
		return projectInfo{}, fmt.Errorf("handshake returned no sid: %s", bodyPreview(body))
	}
	postPath := fmt.Sprintf("/%s/?EIO=%s&transport=polling&projectId=%s&sid=%s&t=%d", socketResource, url.QueryEscape(eio), url.QueryEscape(b.projectID), url.QueryEscape(sid), time.Now().UnixMilli())
	if err := b.engineIOPost(ctx, socketBase, postPath, encodeEngineIOClientPayload(eio, "40")); err != nil {
		return projectInfo{}, err
	}
	pollPath := func() string {
		return fmt.Sprintf("/%s/?EIO=%s&transport=polling&projectId=%s&sid=%s&t=%d", socketResource, url.QueryEscape(eio), url.QueryEscape(b.projectID), url.QueryEscape(sid), time.Now().UnixMilli())
	}
	for attempt := 0; attempt < 8; attempt++ {
		req, err := b.newSocketRequest(ctx, http.MethodGet, socketBase, pollPath(), nil)
		if err != nil {
			return projectInfo{}, err
		}
		resp, err := b.client.Do(req)
		if err != nil {
			return projectInfo{}, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return projectInfo{}, err
		}
		if resp.StatusCode != http.StatusOK {
			return projectInfo{}, fmt.Errorf("poll failed: HTTP %d", resp.StatusCode)
		}
		for _, packet := range decodeEngineIOPayload(string(body)) {
			if packet == "2" {
				_ = b.engineIOPost(ctx, socketBase, postPath, encodeEngineIOClientPayload(eio, "3"))
				continue
			}
			if info, ok := projectInfoFromSocketIOPacket(packet); ok {
				return info, nil
			}
		}
	}
	return projectInfo{}, errors.New("joinProjectResponse not found")
}

func (b *overleafBackend) engineIOPost(ctx context.Context, socketBase, path, payload string) error {
	req, err := b.newSocketRequest(ctx, http.MethodPost, socketBase, path, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("post failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (b *overleafBackend) socketPost(ctx context.Context, socketBase, socketResource, sid, packet string) error {
	return b.socketPostWithProjectID(ctx, socketBase, socketResource, sid, packet, true)
}

func (b *overleafBackend) socketPostWithProjectID(ctx context.Context, socketBase, socketResource, sid, packet string, withProjectID bool) error {
	query := fmt.Sprintf("t=%d", time.Now().UnixMilli())
	if withProjectID {
		query = fmt.Sprintf("projectId=%s&%s", url.QueryEscape(b.projectID), query)
	}
	path := fmt.Sprintf("/%s/1/xhr-polling/%s?%s", socketResource, url.PathEscape(sid), query)
	req, err := b.newSocketRequest(ctx, http.MethodPost, socketBase, path, strings.NewReader(packet))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (b *overleafBackend) socketEndpoint(wsURL string) (string, string) {
	if strings.TrimSpace(wsURL) == "" {
		return b.host, "socket.io"
	}
	parsed, err := url.Parse(wsURL)
	if err != nil {
		return b.host, "socket.io"
	}
	if !parsed.IsAbs() {
		base, err := url.Parse(b.host)
		if err != nil {
			return b.host, "socket.io"
		}
		parsed = base.ResolveReference(parsed)
	}
	if parsed.Scheme == "ws" {
		parsed.Scheme = "http"
	}
	if parsed.Scheme == "wss" {
		parsed.Scheme = "https"
	}
	resource := strings.Trim(parsed.EscapedPath(), "/")
	if resource == "" {
		resource = "socket.io"
	}
	return parsed.Scheme + "://" + parsed.Host, resource
}

func (b *overleafBackend) fetchCSRF(ctx context.Context) error {
	req, err := b.newRequest(ctx, http.MethodGet, "/project", nil)
	if err != nil {
		return err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch csrf failed: HTTP %d", resp.StatusCode)
	}
	csrf := extractCSRF(body)
	if csrf == "" {
		return errors.New("could not find Overleaf CSRF token; cookie may be invalid or UI changed")
	}
	b.csrf = csrf
	return nil
}

func (b *overleafBackend) ensureFolder(ctx context.Context, info projectInfo, rel string) (string, map[string]string, error) {
	if len(info.RootFolder) == 0 {
		return "", nil, errors.New("could not find Overleaf root folder id")
	}
	root := &info.RootFolder[0]
	currentID := root.ID
	current := root
	remoteDirs := map[string]string{}
	prefix := ""
	parts := strings.Split(rel, "/")
	for _, segment := range parts[:len(parts)-1] {
		prefix = joinRemotePath(prefix, segment)
		var found *projectFolder
		for i := range current.Folders {
			if strings.EqualFold(current.Folders[i].Name, segment) {
				found = &current.Folders[i]
				break
			}
		}
		if found != nil {
			currentID = found.ID
			remoteDirs[prefix] = remoteID("folder", found.ID)
			current = found
			continue
		}
		newFolder, err := b.createFolder(ctx, currentID, segment)
		if err != nil {
			return "", nil, err
		}
		current.Folders = append(current.Folders, newFolder)
		currentID = newFolder.ID
		remoteDirs[prefix] = remoteID("folder", newFolder.ID)
		current = &current.Folders[len(current.Folders)-1]
	}
	if len(remoteDirs) == 0 {
		remoteDirs = nil
	}
	return currentID, remoteDirs, nil
}

func (b *overleafBackend) createFolder(ctx context.Context, parentID, name string) (projectFolder, error) {
	payload := map[string]string{"parent_folder_id": parentID, "name": name}
	body, _ := json.Marshal(payload)
	req, err := b.newRequest(ctx, http.MethodPost, fmt.Sprintf("/project/%s/folder", b.projectID), bytes.NewReader(body))
	if err != nil {
		return projectFolder{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Csrf-Token", b.csrf)
	resp, err := b.client.Do(req)
	if err != nil {
		return projectFolder{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return projectFolder{}, fmt.Errorf("create remote folder %s failed: HTTP %d: %s", name, resp.StatusCode, trimBody(respBody))
	}
	var folder projectFolder
	if err := json.Unmarshal(respBody, &folder); err != nil {
		return projectFolder{}, err
	}
	return folder, nil
}

func (b *overleafBackend) deleteEntity(ctx context.Context, id, typ string) error {
	form := url.Values{}
	if b.csrf != "" {
		form.Set("_csrf", b.csrf)
	}
	req, err := b.newRequest(ctx, http.MethodDelete, fmt.Sprintf("/project/%s/%s/%s", b.projectID, typ, id), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Csrf-Token", b.csrf)
	req.Header.Set("X-CSRF-TOKEN", b.csrf)
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("delete remote entity failed: HTTP %d: %s", resp.StatusCode, trimBody(body))
}

func (b *overleafBackend) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	return b.newSocketRequest(ctx, method, b.host, path, body)
}

func (b *overleafBackend) newSocketRequest(ctx context.Context, method, base, path string, body io.Reader) (*http.Request, error) {
	u := base + path
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cookieHeader(b.cookie))
	req.Header.Set("User-Agent", "ovl/0.1")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", fmt.Sprintf("%s/project/%s", b.host, b.projectID))
	return req, nil
}

func (b *overleafBackend) socketCookieHeader(u *url.URL) string {
	values := []string{}
	if cookie := cookieHeader(b.cookie); cookie != "" {
		values = append(values, cookie)
	}
	if b.client != nil && b.client.Jar != nil && u != nil {
		for _, cookie := range b.client.Jar.Cookies(u) {
			values = append(values, cookie.Name+"="+cookie.Value)
		}
	}
	return strings.Join(values, "; ")
}

func snapshotsFromZip(content []byte) (map[string]domain.FileSnapshot, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("remote response is not a zip archive: %w", err)
	}
	out := map[string]domain.FileSnapshot{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(file.Name)
		if strings.HasPrefix(name, "/") || strings.Contains(name, "../") {
			return nil, fmt.Errorf("unsafe path in remote zip: %s", file.Name)
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		sum := sha256.Sum256(data)
		out[name] = domain.FileSnapshot{
			Path:    name,
			Type:    fileType(data),
			SHA256:  hex.EncodeToString(sum[:]),
			Size:    int64(len(data)),
			Content: data,
		}
	}
	return out, nil
}

func extractCSRF(body []byte) string {
	text := string(body)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?is)<meta[^>]+name=["']ol-csrfToken["'][^>]+content=["']([^"']+)["']`),
		regexp.MustCompile(`(?is)<input[^>]+name=["']_csrf["'][^>]+value=["']([^"']+)["']`),
		regexp.MustCompile(`(?is)csrfToken["']?\s*[:=]\s*["']([^"']+)["']`),
	}
	for _, pattern := range patterns {
		match := pattern.FindStringSubmatch(text)
		if len(match) == 2 {
			return html.UnescapeString(match[1])
		}
	}
	return ""
}

func extractWSURL(body []byte) string {
	return extractMetaContent(body, "ol-wsUrl")
}

func extractMetaContent(body []byte, name string) string {
	text := string(body)
	pattern := regexp.MustCompile(`(?is)<meta[^>]+name=["']` + regexp.QuoteMeta(name) + `["'][^>]+content=["']([^"']*)["']`)
	match := pattern.FindStringSubmatch(text)
	if len(match) == 2 {
		return html.UnescapeString(match[1])
	}
	return ""
}

func extractProjectInfo(body []byte) (projectInfo, error) {
	text := string(body)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?is)<meta[^>]+name=["']ol-project["'][^>]+content=["']([^"']+)["']`),
		regexp.MustCompile(`(?is)<script[^>]+id=["']ol-project["'][^>]*>(.*?)</script>`),
	}
	for _, pattern := range patterns {
		match := pattern.FindStringSubmatch(text)
		if len(match) != 2 {
			continue
		}
		projectJSON := html.UnescapeString(match[1])
		if decoded, err := url.QueryUnescape(projectJSON); err == nil && strings.HasPrefix(strings.TrimSpace(decoded), "{") {
			projectJSON = decoded
		}
		var info projectInfo
		if err := json.Unmarshal([]byte(projectJSON), &info); err == nil && len(info.RootFolder) > 0 {
			return info, nil
		}
	}
	return projectInfo{}, errors.New("could not parse Overleaf project file tree; upload/delete endpoints may have changed")
}

func decodeSocketPayload(body string) []string {
	if body == "" {
		return nil
	}
	if !strings.HasPrefix(body, "\ufffd") {
		return []string{body}
	}
	var packets []string
	for len(body) > 0 {
		if !strings.HasPrefix(body, "\ufffd") {
			break
		}
		body = strings.TrimPrefix(body, "\ufffd")
		idx := strings.Index(body, "\ufffd")
		if idx < 0 {
			break
		}
		length, err := strconv.Atoi(body[:idx])
		if err != nil {
			break
		}
		body = body[idx+len("\ufffd"):]
		if length < 0 || length > len(body) {
			break
		}
		packets = append(packets, body[:length])
		body = body[length:]
	}
	return packets
}

func decodeEngineIOPayload(body string) []string {
	if body == "" {
		return nil
	}
	if strings.HasPrefix(body, "\ufffd") {
		return decodeSocketPayload(body)
	}
	if strings.Contains(body, "\x1e") {
		return strings.Split(body, "\x1e")
	}
	var packets []string
	for len(body) > 0 {
		colon := strings.IndexByte(body, ':')
		if colon <= 0 {
			break
		}
		n, err := strconv.Atoi(body[:colon])
		if err != nil || n < 0 || colon+1+n > len(body) {
			break
		}
		body = body[colon+1:]
		packets = append(packets, body[:n])
		body = body[n:]
	}
	if len(packets) > 0 && body == "" {
		return packets
	}
	return []string{body}
}

func engineIOSessionID(body string) string {
	if match := regexp.MustCompile(`"sid"\s*:\s*"([^"]+)"`).FindStringSubmatch(body); len(match) == 2 {
		return match[1]
	}
	for _, packet := range decodeEngineIOPayload(body) {
		packet = strings.TrimSpace(packet)
		if !strings.HasPrefix(packet, "0") {
			continue
		}
		var payload struct {
			SID string `json:"sid"`
		}
		if err := json.Unmarshal([]byte(packet[1:]), &payload); err == nil {
			return payload.SID
		}
	}
	return ""
}

func encodeEngineIOClientPayload(eio, packet string) string {
	if eio == "3" {
		return fmt.Sprintf("%d:%s", len(packet), packet)
	}
	return packet
}

func readWebSocketFrame(r *bufio.Reader) (byte, []byte, error) {
	header, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	opcode := header & 0x0f
	lengthByte, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	masked := lengthByte&0x80 != 0
	length := uint64(lengthByte & 0x7f)
	if length == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		length = uint64(ext[0])<<8 | uint64(ext[1])
	} else if length == 127 {
		ext := make([]byte, 8)
		if _, err := io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		for _, b := range ext {
			length = length<<8 | uint64(b)
		}
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	if length > 16*1024*1024 {
		return 0, nil, fmt.Errorf("websocket frame too large: %d", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

func writeWebSocketText(w io.Writer, payload string) error {
	return writeWebSocketFrame(w, 0x1, []byte(payload))
}

func writeWebSocketFrame(w io.Writer, opcode byte, payload []byte) error {
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header := []byte{0x80 | opcode}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, 0x80|byte(length))
	case length <= 0xffff:
		header = append(header, 0x80|126, byte(length>>8), byte(length))
	default:
		header = append(header, 0x80|127)
		for shift := 56; shift >= 0; shift -= 8 {
			header = append(header, byte(uint64(length)>>shift))
		}
	}
	header = append(header, mask[:]...)
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(masked)
	return err
}

func bodyPreview(body []byte) string {
	text := strings.TrimSpace(string(body))
	text = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return -1
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 160 {
		text = text[:160] + "..."
	}
	if text == "" {
		return "empty response body"
	}
	return "body=" + strconv.Quote(text)
}

func projectInfoFromSocketIOPacket(packet string) (projectInfo, bool) {
	packet = strings.TrimSpace(packet)
	if strings.HasPrefix(packet, "42") {
		var payload []json.RawMessage
		if err := json.Unmarshal([]byte(packet[2:]), &payload); err == nil && len(payload) >= 2 {
			var event string
			if json.Unmarshal(payload[0], &event) == nil && event == "joinProjectResponse" {
				var body struct {
					Project projectInfo `json:"project"`
				}
				if json.Unmarshal(payload[1], &body) == nil && len(body.Project.RootFolder) > 0 {
					return body.Project, true
				}
			}
		}
	}
	if strings.HasPrefix(packet, "5:::") {
		var payload struct {
			Name string `json:"name"`
			Args []struct {
				Project projectInfo `json:"project"`
			} `json:"args"`
		}
		if err := json.Unmarshal([]byte(packet[4:]), &payload); err == nil &&
			payload.Name == "joinProjectResponse" &&
			len(payload.Args) > 0 &&
			len(payload.Args[0].Project.RootFolder) > 0 {
			return payload.Args[0].Project, true
		}
	}
	return projectInfo{}, false
}

func normalizeEntityType(typ string) string {
	switch strings.ToLower(typ) {
	case "doc", "docs", "document":
		return "doc"
	case "file", "fileref", "file_ref", "binary":
		return "file"
	case "folder", "folders", "dir", "directory":
		return "folder"
	default:
		return ""
	}
}

func remoteID(typ, id string) string {
	typ = normalizeEntityType(typ)
	if typ == "" || id == "" {
		return ""
	}
	return typ + ":" + id
}

func randomUUID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return ""
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

func remoteEntityFromState(state domain.FileState) (remoteEntity, bool) {
	typ, id, ok := strings.Cut(state.RemoteID, ":")
	if !ok || id == "" {
		return remoteEntity{}, false
	}
	typ = normalizeEntityType(typ)
	if typ == "" {
		return remoteEntity{}, false
	}
	return remoteEntity{ID: id, Type: typ}, true
}

func computeRootFolderID(projectID string) string {
	if len(projectID) != 24 {
		return ""
	}
	prefix := projectID[:16]
	suffix, err := strconv.ParseUint(projectID[16:], 16, 32)
	if err != nil || suffix == 0 {
		return ""
	}
	return prefix + fmt.Sprintf("%08x", suffix-1)
}

func walkRemoteFolder(folder projectFolder, prefix string, out map[string]remoteEntity) {
	for _, doc := range folder.Docs {
		id := firstNonEmpty(doc.ID, doc.ID2)
		if doc.Name != "" && id != "" {
			out[joinRemotePath(prefix, doc.Name)] = remoteEntity{ID: id, Type: "doc", FolderID: folder.ID}
		}
	}
	for _, file := range folder.Files {
		id := firstNonEmpty(file.ID, file.ID2)
		if file.Name != "" && id != "" {
			out[joinRemotePath(prefix, file.Name)] = remoteEntity{ID: id, Type: "file", FolderID: folder.ID}
		}
	}
	for _, child := range folder.Folders {
		childPath := joinRemotePath(prefix, child.Name)
		if child.Name != "" && child.ID != "" {
			out[childPath] = remoteEntity{ID: child.ID, Type: "folder", FolderID: folder.ID}
		}
		walkRemoteFolder(child, childPath, out)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (l entityLookup) summary() string {
	keys := make([]string, 0, len(l.Entities))
	for path := range l.Entities {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	if len(keys) > 5 {
		keys = append(keys[:5], "...")
	}
	parts := []string{fmt.Sprintf("source=%s", l.Source), fmt.Sprintf("entities=%d", len(l.Entities))}
	if len(keys) > 0 {
		parts = append(parts, "sample="+strings.Join(keys, ","))
	}
	if l.TreeErr != nil {
		parts = append(parts, "projectPage="+l.TreeErr.Error())
	}
	if l.SocketErr != nil {
		parts = append(parts, "socket="+l.SocketErr.Error())
	}
	if l.APIErr != nil {
		parts = append(parts, "entitiesAPI="+l.APIErr.Error())
	}
	return strings.Join(parts, "; ")
}

func joinRemotePath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

func pathBase(path string) string {
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

func mimeType(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "tex", "cls", "sty":
		return "text/x-tex"
	case "bib":
		return "text/x-bibtex"
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "pdf":
		return "application/pdf"
	case "svg":
		return "image/svg+xml"
	case "eps":
		return "application/postscript"
	default:
		return "application/octet-stream"
	}
}

func trimBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}

func cookieHeader(cookie string) string {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return ""
	}
	if strings.Contains(cookie, "=") {
		return cookie
	}
	return "overleaf_session2=" + cookie
}

func fileType(content []byte) string {
	if strings.IndexByte(string(content), 0) >= 0 {
		return "binary"
	}
	return "text"
}
