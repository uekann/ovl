package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimRight(host, "/")
	if host == "" {
		return "https://www.overleaf.com"
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "https://" + host
	}
	return host
}

func loadCookie(root string, cfg Config) (string, error) {
	for _, key := range []string{"OVL_COOKIE", "OVERLEAF_COOKIE"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, nil
		}
	}
	if cfg.AuthFile != "" {
		path := cfg.AuthFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, filepath.FromSlash(path))
		}
		content, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(content)), nil
		}
	}
	path := strings.TrimSpace(os.Getenv("OVL_COOKIE_FILE"))
	if path == "" {
		return "", errors.New("no Overleaf cookie configured; run ovl auth")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}
func defaultProjectAuthFile() string {
	return filepath.ToSlash(filepath.Join(metaDir, "auth", "cookie"))
}

func writeCookieFile(path, cookie string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(cookie)+"\n"), 0o600)
}

func readCookieInteractive() (string, error) {
	fmt.Fprint(os.Stderr, "Overleaf cookie: ")
	if authInput == os.Stdin && term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		cookie := strings.TrimSpace(string(b))
		if cookie == "" {
			return "", errors.New("empty cookie")
		}
		return cookie, nil
	}
	line, err := bufio.NewReader(authInput).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	cookie := strings.TrimSpace(line)
	if cookie == "" {
		return "", errors.New("empty cookie")
	}
	return cookie, nil
}
func defaultIgnore() string {
	return `.git/
.gitignore
.overleaf-sync/
.direnv/
result/
node_modules/
*.aux
*.log
*.synctex.gz
*.fls
*.fdb_latexmk
*.out
*.pdf
`
}
