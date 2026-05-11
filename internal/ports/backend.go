package ports

import (
	"context"

	"ovl/internal/domain"
)

type Backend interface {
	List(context.Context) (map[string]domain.FileSnapshot, error)
	Put(context.Context, domain.FileSnapshot) (domain.FileState, error)
	Delete(context.Context, string, domain.FileState) error
	CompilePDF(context.Context, string) (domain.CompileResult, error)
}
