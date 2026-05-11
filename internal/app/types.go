package app

import (
	"io"
	"os"

	"ovl/internal/domain"
	"ovl/internal/ports"
)

const metaDir = domain.MetaDir

var authInput io.Reader = os.Stdin

type Config = domain.Config
type State = domain.State
type FileState = domain.FileState
type FileSnapshot = domain.FileSnapshot
type CompileResult = domain.CompileResult
type Backend = ports.Backend
