package app

import (
	"context"
	"errors"
	"os"

	"ovl/internal/adapters/overleaf"
)

type errorBackend struct {
	err error
}

func backendFor(root string, cfg Config) Backend {
	cookie, err := loadCookie(root, cfg)
	if err != nil || cookie == "" {
		if err == nil {
			err = errors.New("empty Overleaf cookie; run ovl auth")
		}
		return errorBackend{err: err}
	}
	if cookie != "" {
		host := cfg.Host
		if host == "" {
			host = os.Getenv("OVL_HOST")
		}
		if host == "" {
			host = "https://www.overleaf.com"
		}
		return overleaf.New(normalizeHost(host), cfg.ProjectID, cookie, nil)
	}
	return errorBackend{err: errors.New("no Overleaf cookie configured; run ovl auth")}
}

func (b errorBackend) List(context.Context) (map[string]FileSnapshot, error) {
	return nil, b.err
}

func (b errorBackend) Put(context.Context, FileSnapshot) (FileState, error) {
	return FileState{}, b.err
}

func (b errorBackend) Delete(context.Context, string, FileState) error {
	return b.err
}

func (b errorBackend) CompilePDF(context.Context, string) (CompileResult, error) {
	return CompileResult{}, b.err
}
