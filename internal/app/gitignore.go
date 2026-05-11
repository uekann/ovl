package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func ensureGitIgnore(root string) error {
	path := filepath.Join(root, ".gitignore")
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(path, []byte(metaDir+"/\n"), 0o644)
	}
	if err != nil {
		return err
	}
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == metaDir+"/" || trimmed == metaDir {
			return nil
		}
	}
	next := string(content)
	if next != "" && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	next += metaDir + "/\n"
	return os.WriteFile(path, []byte(next), 0o644)
}

func hasHelp(args []string) bool {
	for _, arg := range args {
		if isHelp(arg) {
			return true
		}
	}
	return false
}

func isHelp(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "help"
}
