# Repository guidance

`ovl` is a Go CLI that synchronizes local files with Overleaf through private web endpoints. Changes that mutate remote state must prefer preserving user data and review history over convenience.

## Verification

Run `make check` before considering a change complete. It checks formatting and module-file consistency, runs `go vet` and Staticcheck, builds every package, and runs every test both normally and with the race detector.

## Code Review Rules

- Preserve Overleaf entity identity and keep synchronization fail-safe. Updating an existing path must use the in-place upload flow, never delete and recreate the document; normal pushes must reject remote changes or conflicts, and destructive remote changes must remain explicit, backed up, and verified before sync state advances.
- Treat comment locations as verified data, not guesses. Overleaf offsets are zero-based UTF-16 offsets; expose line, column, or snippets only after the selected text exactly matches the current remote document, and keep comments for missing, deleted, stale, or otherwise unlocatable documents hidden unless `--include-unanchored` is explicit.
- Protect credentials and fail safely around Overleaf's private APIs. Never expose session cookies through arguments, config or state files, logs, errors, or normal output; keep stored credentials permission-restricted and origin-scoped, and never guess entity IDs or continue an ambiguous remote mutation when endpoints or schemas drift.
