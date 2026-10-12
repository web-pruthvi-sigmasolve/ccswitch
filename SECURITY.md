# Security policy

## What ccswitch reads and writes

ccswitch is a local tool. It makes no network requests and sends no telemetry.

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

## Supported versions

Only the latest release gets security fixes while the project is at v0.x.

## Reporting a vulnerability

Please do not open a public issue for a security problem.

Use GitHub private vulnerability reporting: go to the **Security** tab of
`github.com/web-pruthvi-sigmasolve/ccswitch` and choose **Report a vulnerability**.

Include the version (`ccswitch version`), your OS, and steps to reproduce.
We aim to reply within 7 days and to ship a fix for confirmed issues within 30 days.
