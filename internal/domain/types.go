package domain

import "time"

const MetaDir = ".overleaf-sync"

type Config struct {
	ProjectID  string `json:"project_id"`
	Root       string `json:"root"`
	IgnoreFile string `json:"ignore_file"`
	Host       string `json:"host,omitempty"`
	AuthFile   string `json:"auth_file,omitempty"`
}

type State struct {
	ProjectID string               `json:"project_id"`
	LastSync  string               `json:"last_sync"`
	Files     map[string]FileState `json:"files"`
}

type FileState struct {
	RemoteID  string            `json:"remote_id,omitempty"`
	RemoteDir map[string]string `json:"remote_dir,omitempty"`
	Type      string            `json:"type"`
	SHA256    string            `json:"sha256"`
	Size      int64             `json:"size"`
}

type FileSnapshot struct {
	Path    string
	Type    string
	SHA256  string
	Size    int64
	Content []byte
}

type CompileResult struct {
	PDF    []byte
	Status string
}

type CommentThread struct {
	ID         string           `json:"id"`
	Resolved   bool             `json:"resolved"`
	ResolvedAt *time.Time       `json:"resolved_at,omitempty"`
	Anchor     *CommentAnchor   `json:"anchor,omitempty"`
	Messages   []CommentMessage `json:"messages"`
}

type CommentAnchor struct {
	DocumentID string `json:"document_id,omitempty"`
	Path       string `json:"path"`
	Offset     int    `json:"offset"`
	Text       string `json:"text"`
}

type CommentMessage struct {
	ID        string        `json:"id"`
	Content   string        `json:"content"`
	Timestamp *time.Time    `json:"timestamp,omitempty"`
	Author    CommentAuthor `json:"author"`
}

type CommentAuthor struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}
