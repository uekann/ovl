package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	if isHelp(args[0]) {
		usage()
		return nil
	}
	switch args[0] {
	case "init":
		return cmdInit(ctx, args[1:])
	case "auth":
		return cmdAuth(args[1:])
	case "pull":
		return cmdPull(ctx, args[1:])
	case "status":
		return cmdStatus(ctx, args[1:])
	case "diff":
		return cmdDiff(ctx, args[1:])
	case "compile":
		return cmdCompile(ctx, args[1:])
	case "comments":
		return cmdComments(ctx, args[1:])
	case "push":
		return cmdPush(ctx, args[1:])
	case "backup-remote":
		return cmdBackup(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Println(`ovl - safe local-first Overleaf sync

Commands:
  ovl init <project-id>
  ovl auth [--file <path>]
  ovl pull [--preview] [--force] [--delete-local]
  ovl status [--local-only]
  ovl diff [--remote] [--all]
  ovl compile [--push] [--output <path>] [root.tex]
  ovl comments [--include-resolved] [--json]
  ovl push [--force] [--delete]
  ovl backup-remote

Remote backend:
  ovl uses cookie-authenticated Overleaf endpoints.`)
}

func cmdInit(ctx context.Context, args []string) error {
	if hasHelp(args) {
		fmt.Println("usage: ovl init [--root <path>] [--host <url>] <project-id>")
		return nil
	}
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	root := fs.String("root", ".", "project root")
	host := fs.String("host", "https://www.overleaf.com", "Overleaf host URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: ovl init <project-id>")
	}
	projectID := fs.Arg(0)
	projectRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, metaDir, "backups"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, metaDir, "base"), 0o755); err != nil {
		return err
	}
	cfg, _ := readConfig(projectRoot)
	if cfg.Root == "" {
		cfg.Root = "."
	}
	if cfg.IgnoreFile == "" {
		cfg.IgnoreFile = ".olignore"
	}
	cfg.ProjectID = projectID
	cfg.Host = normalizeHost(*host)
	if cfg.AuthFile == "" {
		cfg.AuthFile = defaultProjectAuthFile()
	}
	if _, err := backendFor(projectRoot, cfg).List(ctx); err != nil {
		return fmt.Errorf("cannot access Overleaf project %s; run ovl auth first and check project id: %w", projectID, err)
	}
	if err := writeJSONAtomic(filepath.Join(projectRoot, metaDir, "config.json"), cfg); err != nil {
		return err
	}
	state := State{ProjectID: projectID, LastSync: time.Now().Format(time.RFC3339), Files: map[string]FileState{}}
	if err := writeState(projectRoot, state, map[string]FileSnapshot{}); err != nil {
		return err
	}
	ignorePath := filepath.Join(projectRoot, ".olignore")
	if _, err := os.Stat(ignorePath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(ignorePath, []byte(defaultIgnore()), 0o644); err != nil {
			return err
		}
	}
	if err := ensureGitIgnore(projectRoot); err != nil {
		return err
	}
	fmt.Printf("Initialized ovl project %s\n", projectID)
	return nil
}

func cmdAuth(args []string) error {
	if hasHelp(args) {
		fmt.Println("usage: ovl auth [--file <path>]\n\nReads the Overleaf session cookie interactively.")
		return nil
	}
	if len(args) > 0 && args[0] == "cookie" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("auth cookie", flag.ContinueOnError)
	fs.Bool("local", false, "deprecated; project-local auth is now the default")
	filePath := fs.String("file", "", "store cookie in this file and configure this project to use it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: ovl auth [--file <path>]  # cookie is read interactively")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, _ := readConfig(root)
	if cfg.Root == "" {
		cfg.Root = "."
	}
	if cfg.IgnoreFile == "" {
		cfg.IgnoreFile = ".olignore"
	}
	cookie, err := readCookieInteractive()
	if err != nil {
		return err
	}
	path := filepath.Join(root, metaDir, "auth", "cookie")
	cfg.AuthFile = defaultProjectAuthFile()
	if *filePath != "" {
		path = *filePath
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		rel, err := filepath.Rel(root, path)
		if err == nil && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			cfg.AuthFile = filepath.ToSlash(rel)
		} else {
			cfg.AuthFile = path
		}
	}
	if err := writeCookieFile(path, cookie); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(root, metaDir, "config.json"), cfg); err != nil {
		return err
	}
	if err := ensureGitIgnore(root); err != nil {
		return err
	}
	fmt.Printf("Cookie saved to %s\n", path)
	return nil
}

func cmdPull(ctx context.Context, args []string) error {
	if hasHelp(args) {
		fmt.Println("usage: ovl pull [--preview] [--force] [--delete-local]")
		return nil
	}
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	preview := fs.Bool("preview", false, "show what would change")
	force := fs.Bool("force", false, "overwrite local changes and delete local files missing remotely")
	deleteLocal := fs.Bool("delete-local", false, "delete local files removed remotely")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, cfg, state, err := loadProject()
	if err != nil {
		return err
	}
	local, err := scanLocal(root, cfg)
	if err != nil {
		return err
	}
	remote, err := backendFor(root, cfg).List(ctx)
	if err != nil {
		return err
	}
	report := compare(state.Files, local, remote)
	if len(report.Conflicts) > 0 && !*force {
		printPaths("Conflict detected:", report.Conflicts)
		fmt.Println("\nBoth local and remote changed since last sync.")
		return errors.New("refusing to overwrite local files")
	}
	writes := pullWrites(state.Files, local, remote, *force)
	deletes := pullDeletes(state.Files, local, remote, *deleteLocal, *force)
	if *preview {
		printPaths("Would update/create:", writes)
		if *deleteLocal || *force {
			printPaths("Would delete locally:", deletes)
		} else {
			printRemoteDeleted(state.Files, local, remote)
		}
		return nil
	}
	if *force {
		if _, err := backupLocal(root, local); err != nil {
			return fmt.Errorf("local backup failed; refusing force pull: %w", err)
		}
	}
	for _, p := range writes {
		if err := writeProjectFile(root, p, remote[p].Content); err != nil {
			return err
		}
	}
	for _, p := range deletes {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(p))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	nextFiles := statesFromSnapshots(remote)
	mergeRemoteIDs(nextFiles, state.Files)
	if err := writeState(root, State{ProjectID: state.ProjectID, LastSync: time.Now().Format(time.RFC3339), Files: nextFiles}, remote); err != nil {
		return err
	}
	fmt.Printf("Pulled %d file(s)\n", len(writes)+len(deletes))
	return nil
}

func cmdStatus(ctx context.Context, args []string) error {
	if hasHelp(args) {
		fmt.Println("usage: ovl status [--local-only]")
		return nil
	}
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	localOnly := fs.Bool("local-only", false, "do not fetch remote state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, cfg, state, err := loadProject()
	if err != nil {
		return err
	}
	local, err := scanLocal(root, cfg)
	if err != nil {
		return err
	}
	remote := map[string]FileSnapshot{}
	if !*localOnly {
		remote, err = backendFor(root, cfg).List(ctx)
		if err != nil {
			return err
		}
	}
	if *localOnly {
		printLocalStatus(state.Files, local)
		return nil
	}
	report := compare(state.Files, local, remote)
	printPaths("Local changes:", report.Local)
	printPaths("Remote changes:", report.Remote)
	printPaths("Conflicts:", report.Conflicts)
	if len(report.Local) == 0 && len(report.Remote) == 0 && len(report.Conflicts) == 0 {
		fmt.Println("Clean")
	}
	return nil
}

func cmdDiff(ctx context.Context, args []string) error {
	if hasHelp(args) {
		fmt.Println("usage: ovl diff [--remote] [--all]")
		return nil
	}
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	remoteFlag := fs.Bool("remote", false, "diff remote against base")
	all := fs.Bool("all", false, "show local and remote diff")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, cfg, _, err := loadProject()
	if err != nil {
		return err
	}
	if !*remoteFlag || *all {
		local, err := scanLocal(root, cfg)
		if err != nil {
			return err
		}
		fmt.Println("Local diff:")
		printDiff(root, local)
	}
	if *remoteFlag || *all {
		remote, err := backendFor(root, cfg).List(ctx)
		if err != nil {
			return err
		}
		fmt.Println("Remote diff:")
		printDiff(root, remote)
	}
	return nil
}

func cmdCompile(ctx context.Context, args []string) error {
	if hasHelp(args) {
		fmt.Println("usage: ovl compile [--push] [--output <path>] [root.tex]")
		return nil
	}
	fs := flag.NewFlagSet("compile", flag.ContinueOnError)
	push := fs.Bool("push", false, "push local changes before compiling")
	output := fs.String("output", "", "write PDF to this path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: ovl compile [--push] [--output <path>] [root.tex]")
	}
	rootDoc := ""
	if fs.NArg() == 1 {
		rootDoc = filepath.ToSlash(fs.Arg(0))
	}
	projectRoot, cfg, _, err := loadProject()
	if err != nil {
		return err
	}
	if *push {
		if err := cmdPush(ctx, nil); err != nil {
			return err
		}
	}
	result, err := backendFor(projectRoot, cfg).CompilePDF(ctx, rootDoc)
	if err != nil {
		return err
	}
	outputPath := *output
	if outputPath == "" {
		outputPath = defaultPDFOutputPath(rootDoc)
	}
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(projectRoot, outputPath)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, result.PDF, 0o644); err != nil {
		return err
	}
	fmt.Printf("Compiled PDF saved to %s\n", outputPath)
	return nil
}

func defaultPDFOutputPath(rootDoc string) string {
	if rootDoc == "" {
		return "output.pdf"
	}
	base := filepath.Base(filepath.FromSlash(rootDoc))
	ext := filepath.Ext(base)
	if ext == "" {
		return base + ".pdf"
	}
	return strings.TrimSuffix(base, ext) + ".pdf"
}

func cmdComments(ctx context.Context, args []string) error {
	if hasHelp(args) {
		fmt.Println("usage: ovl comments [--include-resolved] [--json]")
		return nil
	}
	fs := flag.NewFlagSet("comments", flag.ContinueOnError)
	includeResolved := fs.Bool("include-resolved", false, "include resolved comment threads")
	asJSON := fs.Bool("json", false, "print comments as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: ovl comments [--include-resolved] [--json]")
	}
	root, cfg, _, err := loadProject()
	if err != nil {
		return err
	}
	comments, err := backendFor(root, cfg).ListComments(ctx)
	if err != nil {
		return err
	}
	return writeComments(os.Stdout, filterComments(comments, *includeResolved), *asJSON)
}

func cmdPush(ctx context.Context, args []string) error {
	if hasHelp(args) {
		fmt.Println("usage: ovl push [--force] [--delete]")
		return nil
	}
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite remote changes")
	deleteRemote := fs.Bool("delete", false, "delete remote files removed locally")
	fs.Bool("yes", false, "deprecated; accepted for compatibility")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, cfg, state, err := loadProject()
	if err != nil {
		return err
	}
	local, err := scanLocal(root, cfg)
	if err != nil {
		return err
	}
	backend := backendFor(root, cfg)
	remote, err := backend.List(ctx)
	if err != nil {
		return err
	}
	report := compare(state.Files, local, remote)
	if !*force && (len(report.Remote) > 0 || len(report.Conflicts) > 0) {
		fmt.Println("Remote has changed since last sync.")
		fmt.Println("Refusing to push to avoid overwriting remote edits.")
		printPaths("\nChanged remotely:", report.Remote)
		printPaths("\nConflicts:", report.Conflicts)
		fmt.Println("\nRun:\n  ovl diff --remote\n  ovl pull --preview\n  ovl pull")
		return errors.New("push refused")
	}
	shouldDeleteRemote := *deleteRemote || *force
	if *force {
		if _, err := backupRemote(root, remote); err != nil {
			return fmt.Errorf("backup failed; refusing force push: %w", err)
		}
	}
	if *deleteRemote && !*force {
		if _, err := backupRemote(root, remote); err != nil {
			return fmt.Errorf("backup failed; refusing delete push: %w", err)
		}
	}
	uploads := pushUploads(state.Files, local, remote, *force)
	deletes := pushDeletes(state.Files, local, remote, *deleteRemote, *force)
	deleteDirs := pushDeletedDirs(local, remote, deletes)
	uploadedStates := map[string]FileState{}
	for _, p := range uploads {
		fileState, err := backend.Put(ctx, local[p])
		if err != nil {
			return fmt.Errorf("push partially failed at %s: %w", p, err)
		}
		uploadedStates[p] = fileState
	}
	for _, p := range deletes {
		if err := backend.Delete(ctx, p, state.Files[p]); err != nil {
			return fmt.Errorf("delete partially failed at %s: %w", p, err)
		}
	}
	for _, p := range deleteDirs {
		if err := backend.Delete(ctx, p+"/", dirStateForDelete(p, state.Files)); err != nil {
			return fmt.Errorf("delete directory partially failed at %s: %w", p, err)
		}
	}
	after, err := backend.List(ctx)
	if err != nil {
		return err
	}
	if len(deletes) > 0 {
		var remaining []string
		for _, p := range deletes {
			if _, ok := after[p]; ok {
				remaining = append(remaining, p)
			}
		}
		if len(remaining) > 0 {
			printPaths("Remote file still exists after delete:", remaining)
			return errors.New("delete verification failed")
		}
	}
	nextFiles := statesFromSnapshots(after)
	mergeRemoteIDs(nextFiles, state.Files)
	mergeRemoteIDs(nextFiles, uploadedStates)
	if err := writeState(root, State{ProjectID: state.ProjectID, LastSync: time.Now().Format(time.RFC3339), Files: nextFiles}, after); err != nil {
		return err
	}
	fmt.Printf("Pushed %d file(s)", len(uploads))
	if shouldDeleteRemote {
		fmt.Printf(", deleted %d remote file(s)", len(deletes))
		if len(deleteDirs) > 0 {
			fmt.Printf(", deleted %d remote directory(s)", len(deleteDirs))
		}
	}
	fmt.Println()
	if !shouldDeleteRemote {
		printLocalDeleted(state.Files, local, remote)
	}
	return nil
}

func cmdBackup(ctx context.Context, args []string) error {
	if hasHelp(args) {
		fmt.Println("usage: ovl backup-remote")
		return nil
	}
	fs := flag.NewFlagSet("backup-remote", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, cfg, _, err := loadProject()
	if err != nil {
		return err
	}
	remote, err := backendFor(root, cfg).List(ctx)
	if err != nil {
		return err
	}
	path, err := backupRemote(root, remote)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
