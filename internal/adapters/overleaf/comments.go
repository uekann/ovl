package overleaf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"ovl/internal/domain"
)

type rawCommentThread struct {
	ID         string              `json:"id"`
	Messages   []rawCommentMessage `json:"messages"`
	Resolved   bool                `json:"resolved"`
	ResolvedAt json.RawMessage     `json:"resolved_at"`
}

type rawCommentMessage struct {
	ID        string          `json:"id"`
	Content   string          `json:"content"`
	Timestamp json.RawMessage `json:"timestamp"`
	UserID    string          `json:"user_id"`
	User      *rawCommentUser `json:"user"`
}

type rawCommentUser struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
}

type rawDocumentRanges struct {
	ID     string `json:"id"`
	Ranges struct {
		Comments []rawCommentRange `json:"comments"`
	} `json:"ranges"`
}

type rawCommentRange struct {
	ID string `json:"id"`
	Op struct {
		Content  string `json:"c"`
		Position *int   `json:"p"`
		ThreadID string `json:"t"`
	} `json:"op"`
}

// ListComments returns every Overleaf comment thread. Resolved threads are
// deliberately included here so callers can choose whether to display them.
func (b *overleafBackend) ListComments(ctx context.Context) ([]domain.CommentThread, error) {
	if b.projectID == "" {
		return nil, errors.New("missing project id; run ovl init <project-id>")
	}

	rawThreads, err := b.commentThreads(ctx)
	if err != nil {
		return nil, err
	}
	threads := make(map[string]domain.CommentThread, len(rawThreads))
	for id, rawThread := range rawThreads {
		threadID := firstNonEmpty(id, rawThread.ID)
		if threadID == "" {
			continue
		}
		thread := domain.CommentThread{
			ID:       threadID,
			Resolved: rawThread.Resolved,
			Messages: make([]domain.CommentMessage, 0, len(rawThread.Messages)),
		}
		if resolvedAt, ok := parseCommentTime(rawThread.ResolvedAt); ok {
			thread.ResolvedAt = &resolvedAt
		}
		for _, rawMessage := range rawThread.Messages {
			author := domain.CommentAuthor{ID: rawMessage.UserID}
			if rawMessage.User != nil {
				author.ID = firstNonEmpty(rawMessage.User.ID, author.ID)
				author.Name = strings.TrimSpace(rawMessage.User.Name)
				if author.Name == "" {
					author.Name = strings.TrimSpace(strings.Join([]string{rawMessage.User.FirstName, rawMessage.User.LastName}, " "))
				}
				author.Email = rawMessage.User.Email
			}
			var timestamp *time.Time
			if parsed, ok := parseCommentTime(rawMessage.Timestamp); ok {
				timestamp = &parsed
			}
			thread.Messages = append(thread.Messages, domain.CommentMessage{
				ID:        rawMessage.ID,
				Content:   rawMessage.Content,
				Timestamp: timestamp,
				Author:    author,
			})
		}
		threads[threadID] = thread
	}

	ranges, err := b.commentRanges(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// Threads contain the complete conversation. Ranges only enrich them
		// with document positions and are unavailable on some Overleaf
		// deployments, so degrade to detached threads rather than hiding all
		// comments.
		ranges = nil
	}
	docPaths := make(map[string]string)
	if len(ranges) > 0 {
		// Path resolution is supplemental. If Overleaf changes the project
		// tree endpoint, the selected text and comment conversation are still
		// useful and should remain available to the caller.
		_, lookup, lookupErr := b.projectInfo(ctx)
		if lookupErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
		} else {
			for path, entity := range lookup.Entities {
				if entity.Type == "doc" && entity.ID != "" {
					docPaths[entity.ID] = path
				}
			}
		}
	}
	for _, document := range ranges {
		path := docPaths[document.ID]
		for _, commentRange := range document.Ranges.Comments {
			// Overleaf can leave a zero-length range behind after its selected
			// text is removed. It no longer identifies a trustworthy position.
			if commentRange.Op.Content == "" || commentRange.Op.Position == nil {
				continue
			}
			threadID := firstNonEmpty(commentRange.Op.ThreadID, commentRange.ID)
			if threadID == "" {
				continue
			}
			thread, ok := threads[threadID]
			if !ok {
				// Overleaf's UI also ignores ranges whose conversation no longer
				// exists; synthesizing one here would create a blank comment.
				continue
			}
			anchor := &domain.CommentAnchor{
				DocumentID: document.ID,
				Path:       path,
				Offset:     *commentRange.Op.Position,
				Text:       commentRange.Op.Content,
			}
			if thread.Anchor == nil || commentAnchorLess(anchor, thread.Anchor) {
				thread.Anchor = anchor
			}
			threads[threadID] = thread
		}
	}

	result := make([]domain.CommentThread, 0, len(threads))
	for _, thread := range threads {
		result = append(result, thread)
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.Anchor == nil || right.Anchor == nil {
			if left.Anchor == nil && right.Anchor != nil {
				return false
			}
			if left.Anchor != nil && right.Anchor == nil {
				return true
			}
			return left.ID < right.ID
		}
		if (left.Anchor.Path == "") != (right.Anchor.Path == "") {
			return left.Anchor.Path != ""
		}
		if left.Anchor.Path != right.Anchor.Path {
			return left.Anchor.Path < right.Anchor.Path
		}
		if left.Anchor.DocumentID != right.Anchor.DocumentID {
			return left.Anchor.DocumentID < right.Anchor.DocumentID
		}
		if left.Anchor.Offset != right.Anchor.Offset {
			return left.Anchor.Offset < right.Anchor.Offset
		}
		return left.ID < right.ID
	})
	return result, nil
}

func (b *overleafBackend) commentThreads(ctx context.Context) (map[string]rawCommentThread, error) {
	body, err := b.getCommentJSON(ctx, fmt.Sprintf("/project/%s/threads", b.projectID), "fetch comment threads")
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, fmt.Errorf("decode comment threads: %w", err)
	}
	if wrapped, ok := object["threads"]; ok {
		body = wrapped
	}
	var threads map[string]rawCommentThread
	if err := json.Unmarshal(body, &threads); err != nil {
		return nil, fmt.Errorf("decode comment threads: %w", err)
	}
	if threads == nil {
		threads = map[string]rawCommentThread{}
	}
	return threads, nil
}

func (b *overleafBackend) commentRanges(ctx context.Context) ([]rawDocumentRanges, error) {
	body, err := b.getCommentJSON(ctx, fmt.Sprintf("/project/%s/ranges", b.projectID), "fetch comment ranges")
	if err != nil {
		return nil, err
	}
	var ranges []rawDocumentRanges
	if err := json.Unmarshal(body, &ranges); err == nil {
		return ranges, nil
	}
	var wrapped struct {
		Docs   []rawDocumentRanges `json:"docs"`
		Ranges []rawDocumentRanges `json:"ranges"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("decode comment ranges: %w", err)
	}
	if wrapped.Docs != nil {
		return wrapped.Docs, nil
	}
	if wrapped.Ranges != nil {
		return wrapped.Ranges, nil
	}
	return nil, errors.New("decode comment ranges: response contains neither docs nor ranges")
}

func (b *overleafBackend) getCommentJSON(ctx context.Context, path, operation string) ([]byte, error) {
	req, err := b.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
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
		return nil, fmt.Errorf("%s failed: HTTP %d: %s", operation, resp.StatusCode, trimBody(body))
	}
	return body, nil
}

func parseCommentTime(raw json.RawMessage) (time.Time, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return time.Time{}, false
	}
	if raw[0] == '"' {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return time.Time{}, false
		}
		value = strings.TrimSpace(value)
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, value); err == nil {
				return parsed.UTC(), true
			}
		}
		raw = []byte(value)
	}
	numeric := string(raw)
	if !strings.ContainsAny(numeric, ".eE") {
		if value, err := strconv.ParseInt(numeric, 10, 64); err == nil {
			if value > 100_000_000_000 || value < -100_000_000_000 {
				return time.UnixMilli(value).UTC(), true
			}
			return time.Unix(value, 0).UTC(), true
		}
	}
	value, err := strconv.ParseFloat(numeric, 64)
	if err != nil {
		return time.Time{}, false
	}
	seconds := value
	if value > 100_000_000_000 || value < -100_000_000_000 {
		seconds = value / 1000
	}
	whole := int64(seconds)
	nanos := int64((seconds - float64(whole)) * float64(time.Second))
	return time.Unix(whole, nanos).UTC(), true
}

func commentAnchorLess(left, right *domain.CommentAnchor) bool {
	if (left.Path == "") != (right.Path == "") {
		return left.Path != ""
	}
	if left.Path != right.Path {
		return left.Path < right.Path
	}
	if left.DocumentID != right.DocumentID {
		return left.DocumentID < right.DocumentID
	}
	if left.Offset != right.Offset {
		return left.Offset < right.Offset
	}
	return left.Text < right.Text
}
