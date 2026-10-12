# Security policy

## What ccswitch reads and writes

ccswitch is a local tool. It sends no telemetry. Its only network use is the
update check and `ccswitch update`, described under **Network** below.

**Reads**

- The Claude Desktop user-data folder: `claude-code-sessions/<account>/<org>/local_*.json`
  (the sidebar index), `config.json` (to find the current account id) and
  `logs/main.log` (to find the last signed-in account id).
- The Claude Code transcript folder `~/.claude/projects` (or `CLAUDE_CONFIG_DIR`).
  It only checks that a transcript file exists. It does not read transcript contents.
- Its own config folder (labels, preferences, backups).

**Writes**

- `local_*.json` files in the destination account's folder, and only for chats you selected.
  Each write goes to a temp file, then a rename.
- A backup copy of every file before it is overwritten, under the ccswitch config folder.
- Its own config folder: `accounts.json` (your labels), preferences, backups, lock file.

**Never**

- It never deletes chat files.
- It never touches `scheduled-tasks.json`, transcripts, tokens, cookies or credentials.
- It never starts or kills Claude Desktop.
- It never prints tokens, emails or file contents.

## Network

Two endpoints are ever contacted, both only for updates:

1. `https://api.github.com/repos/web-pruthvi-sigmasolve/ccswitch/releases/latest`:
   the version check. One `GET` at most every 24 hours (and not again for an hour
   after a failure), 3 second timeout, run in the background. The only headers
   are `User-Agent: ccswitch/<version>` and `Accept: application/vnd.github+json`.
   Nothing about you or your chats is sent. The result is cached in
   `update-check.json` in the ccswitch config folder. It is skipped in dev
   builds, in CI (`CI` set), when stderr is not a terminal, with `--json` or
   `--quiet`, and when turned off (`ccswitch update --disable-check`,
   `CCSWITCH_NO_UPDATE_CHECK=1` or `--no-update-check`).
2. `https://github.com/web-pruthvi-sigmasolve/ccswitch/releases/download/...`:
   downloads made by `ccswitch update` (the release archive and `checksums.txt`).
   Redirects are followed only to GitHub's own download hosts over HTTPS.

`ccswitch update` verifies the archive's SHA-256 against `checksums.txt` from the
same release before extracting anything. It extracts only the `ccswitch` binary
from the archive root (entries with a path, `..` or a link are rejected, size
capped at 200 MB), writes it next to the current program and swaps it in with a
rename. On any failure the installed program is left untouched. Note that
`checksums.txt` is served from the same release as the archive, so this
protects against corrupt or tampered downloads but not against a compromised
release; verify releases yourself if that is in your threat model.

## Supported versions

Only the latest release gets security fixes while the project is at v0.x.

## Reporting a vulnerability

Please do not open a public issue for a security problem.

Use GitHub private vulnerability reporting: go to the **Security** tab of
`github.com/web-pruthvi-sigmasolve/ccswitch` and choose **Report a vulnerability**.

Include the version (`ccswitch version`), your OS, and steps to reproduce.
We aim to reply within 7 days and to ship a fix for confirmed issues within 30 days.
