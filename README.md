# ccswitch

[![CI](https://github.com/web-pruthvi-sigmasolve/ccswitch/actions/workflows/ci.yml/badge.svg)](https://github.com/web-pruthvi-sigmasolve/ccswitch/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/web-pruthvi-sigmasolve/ccswitch)](https://github.com/web-pruthvi-sigmasolve/ccswitch/releases/latest)
[![License: MIT](https://img.shields.io/github/license/web-pruthvi-sigmasolve/ccswitch)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/web-pruthvi-sigmasolve/ccswitch)](https://goreportcard.com/report/github.com/web-pruthvi-sigmasolve/ccswitch)

**Switch Claude Desktop accounts without losing your Claude Code chats.**

Claude Desktop shows Claude Code chats per account. Sign in to a different account and your chats seem to vanish. They are still on disk. ccswitch makes the ones you pick visible in the other account.

- You choose which chats to move.
- It never overwrites blindly. Conflicts are detected and shown.
- It backs up every file before writing. `ccswitch undo` restores it.
- No telemetry. The only network use is an optional once-a-day version check against GitHub, which you can turn off. Nothing is ever deleted.

![ccswitch demo](docs/demo.gif)

<!-- docs/demo.gif is recorded with `make demo` (needs https://github.com/charmbracelet/vhs). -->

## What it does

Claude Desktop keeps a small sidebar index of Claude Code chats for each signed-in account. The chat transcripts themselves are shared on your machine and do not belong to any account. So when you sign out of account A and into account B, the sidebar for B is empty even though the data is still there.

ccswitch copies the small index entries from one account to the other. You pick the chats. If the destination already has a chat with the same id, ccswitch compares the two and keeps whichever is newer, field by field. Before any write it saves a backup. If you change your mind, `ccswitch undo` puts everything back.

## Install

### Linux and macOS

```sh
curl -fsSL https://raw.githubusercontent.com/web-pruthvi-sigmasolve/ccswitch/main/install.sh | sh
```

Installs to `~/.local/bin` (override with `CCSWITCH_INSTALL_DIR`). Pick a version with `CCSWITCH_VERSION=v0.1.0`. For `/usr/local/bin` use `sh -s -- --system`. The script verifies the SHA-256 checksum before installing.

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/web-pruthvi-sigmasolve/ccswitch/main/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\Programs\ccswitch` and adds it to your user PATH. Pick a version with `$env:CCSWITCH_VERSION = "v0.1.0"` first.

### Homebrew

```sh
brew install web-pruthvi-sigmasolve/tap/ccswitch
```

### Scoop

```powershell
scoop bucket add ccswitch https://github.com/web-pruthvi-sigmasolve/scoop-bucket
scoop install ccswitch
```

### winget

```powershell
winget install ccswitch.ccswitch
```

### Go

```sh
go install github.com/web-pruthvi-sigmasolve/ccswitch/cmd/ccswitch@latest
```

Needs Go 1.22 or newer.

### Manual download

Get the archive for your system from the [releases page](https://github.com/web-pruthvi-sigmasolve/ccswitch/releases/latest). Files are named `ccswitch_<version>_<os>_<arch>`. Check it against `checksums.txt`, unpack it and put `ccswitch` on your PATH. `.deb`, `.rpm` and `.apk` packages are there too.

## Updating

```sh
ccswitch update            # check, then ask before installing
ccswitch update --check    # only check
```

`ccswitch update` downloads the release archive for your system from GitHub Releases, verifies its SHA-256 checksum against `checksums.txt`, and replaces the running program. Nothing is changed if the checksum does not match. Use `--yes` to skip the question (needed outside a terminal) and `--version v0.2.0` to install a specific version.

If you installed with a package manager, `ccswitch update` prints the right command instead of replacing the file:

| Installed with | Update with |
|---|---|
| Homebrew | `brew upgrade ccswitch` |
| Scoop | `scoop update ccswitch` |
| winget | `winget upgrade ccswitch.ccswitch` |
| `.deb` / `.rpm` | `sudo apt upgrade ccswitch` / `sudo dnf upgrade ccswitch` |
| `go install` | `go install github.com/web-pruthvi-sigmasolve/ccswitch/cmd/ccswitch@latest` |

### The version check

Interactive commands do a quiet background check at most once every 24 hours. If a newer version exists, one line is printed on stderr after the command finishes:

```
ccswitch 0.2.0 is available (you have 0.1.1). Run: ccswitch update
```

What is sent: one HTTPS `GET` to `https://api.github.com/repos/web-pruthvi-sigmasolve/ccswitch/releases/latest`. The only headers are `User-Agent: ccswitch/<version>` and `Accept`. There is no account, machine or usage data. The request has a 3 second timeout, never delays or fails a command, and the answer is cached in `update-check.json` in the ccswitch config folder. After a failed attempt it waits an hour before trying again.

The check never runs in development builds, when stderr is not a terminal, with `--json` or `--quiet`, or when `CI` is set. To turn it off:

- `ccswitch update --disable-check` (saved in the config; `--enable-check` turns it back on)
- `CCSWITCH_NO_UPDATE_CHECK=1` in your environment
- `--no-update-check` for a single run

`ccswitch version` shows the last known latest version without using the network. `ccswitch doctor` shows whether the check is on.

## Usage

### The guided way

Run `ccswitch` with no arguments in a terminal.

1. **Accounts.** Pick the account to copy from. Press `l` to give an account a name.
2. **Direction.** Pick the account to copy to.
3. **Pick chats.** Chats are grouped by project. Move with the arrow keys or `j`/`k` (`pgup`/`pgdn` for pages). `space` toggles a chat, or a whole project when the cursor is on its header. `enter` goes to the next step (Review) wherever the cursor is, once at least one chat is selected. `a` selects every chat that matches the filter, `n` clears them, `/` filters by title or project, `p` collapses a project, `tab`/`shift+tab` jump between projects. `c` cycles what happens to a chat whose copy in the destination is newer: the default policy, keep the destination, use the source.
4. **Review.** This is a dry run. You see what will be added, merged or kept, and where the backup goes. Nothing is written yet. `m` cycles the mode (merge, overwrite, add-only), `c` the conflict policy (keep-dest, prefer-source, skip), `o` includes chats whose transcript is missing, `s` shows the skipped ones. Press `enter` to apply.
5. **Done.** Switch account in Claude Desktop (or restart it) to refresh the sidebar. Press `r` to start again.

Keys: `enter` next, `esc` back, `q` quit, `?` shows or hides the full key list, `ctrl+c` quits from anywhere without writing. Use `--ascii` if your terminal draws the symbols badly.

### Commands

| Command | What it does |
|---|---|
| `ccswitch` | Start the guided TUI. |
| `ccswitch list [--json]` | Show accounts with labels and a short fingerprint. |
| `ccswitch chats --account <n\|uuid\|label>` | List chats for one account, grouped by project. Filters: `--project` (repeatable), `--since 7d`, `--archived` (also list archived chats; they are hidden by default and a note says how many), `--org <uuid>`, `--json`. |
| `ccswitch sync --from A --to B` | Copy chats from account A to B. Choose with `--all`, `--project X`, `--since 7d`, `--id` or `--title "text"`. |
| `ccswitch unarchive --account A` | Un-archive chats in one account. Choose with `--all`, `--id` or `--project`. Supports `--org`, `--dry-run` and `--yes`. |
| `ccswitch label` | Show the names you have given. |
| `ccswitch label <uuid\|n> "<name>"` | Give an account a readable name. `ccswitch label --clear <uuid\|n>` removes it. |
| `ccswitch undo [--list \| --set <id>]` | Restore the last backup, list backups, or restore a chosen one. Supports `--dry-run` and `--yes`. |
| `ccswitch doctor` | Print store paths, candidates, versions and permissions. Use it in bug reports. |
| `ccswitch update [--check] [--yes] [--version vX.Y.Z]` | Check for a newer release and install it. `--enable-check` and `--disable-check` turn the daily background check on or off. |
| `ccswitch version` | Print the version, and the latest known version if a check has run. |
| `ccswitch completion bash\|zsh\|fish\|powershell` | Print a shell completion script. |

An account can be given as its number in `ccswitch list`, a full UUID, a unique UUID prefix (6 or more characters) or its name.

Options for every command:

| Option | Meaning |
|---|---|
| `--user-data <dir>` | Use this Claude Desktop user-data folder instead of auto-detecting it. |
| `--config-dir <dir>` | Use this folder for ccswitch's own data (names, backups, lock). |
| `--json` | Machine-readable output for `list`, `chats`, `sync` and `undo --list`. |
| `--no-color`, `--ascii` | Plain output. `NO_COLOR` is honoured too. |
| `--quiet` | Hide notes and progress lines. |
| `--no-update-check` | Skip the background version check for this run. |

`sync` options:

| Option | Meaning |
|---|---|
| `--org <uuid>` | Destination organisation. Default: the same organisation as the source, else the destination's most active one. |
| `--from-org <uuid>` | Source organisation. Default: the source account's most active one. |
| `--skip-archived` | Leave archived chats out. They are included by default in every selection (`--all`, `--project`, `--since`, `--title`, `--id`). |
| `--no-strip-account-config` | Keep connector settings on newly added chats instead of clearing them. |
| `--mode merge` | Default. Merge source fields onto the destination entry. |
| `--mode overwrite` | Byte copy. Replaces the destination entry. |
| `--mode add-only` | Only add chats that are missing. Never touch existing ones. |
| `--on-conflict keep-dest` | Default. When the destination is newer, keep it. |
| `--on-conflict prefer-source` | Take the source version. |
| `--on-conflict skip` | Leave conflicting chats alone. |
| `--include-orphans` | Also copy chats whose transcript file is missing. |
| `--dry-run` | Show the plan. Write nothing. |
| `--yes` | Do not ask for confirmation. Required when not in a terminal, unless `--dry-run`. |

Exit codes: `0` ok, `1` user error, `2` nothing to do (or nothing matched, or you declined), `3` partial failure, `4` store not found.

`ccswitch undo` puts back the files that were changed. Chats that a run newly added stay where they are, because ccswitch never deletes chats.

### Examples

Name your accounts once:

```sh
ccswitch list
ccswitch label 1 "work"
ccswitch label 2 "personal"
```

See what would happen for one project, without writing anything:

```sh
ccswitch sync --from work --to personal --project ShopifyInforConnector --dry-run
```

Move everything from the last week, and prefer the source when both sides changed:

```sh
ccswitch sync --from work --to personal --since 7d --on-conflict prefer-source --yes
```

Only add missing chats, never touch existing ones:

```sh
ccswitch sync --from personal --to work --all --mode add-only --yes
```

Changed your mind:

```sh
ccswitch undo --list
ccswitch undo
```

## How it works

Claude Desktop stores the sidebar index per account and organisation:

```
<user-data>/claude-code-sessions/<account-uuid>/<org-uuid>/local_<id>.json
```

The transcripts are stored once and are not tied to an account:

```
~/.claude/projects/<encoded-project-path>/<session-id>.jsonl
```

ccswitch reads the index files, checks that each transcript exists (it only checks that the file is there), and writes index entries into the destination account's folder. That is all it needs to do for the chat to show up.

The default user-data folders:

| OS | Folder |
|---|---|
| Linux | `~/.config/Claude` (also flatpak and snap locations) |
| Windows | `%APPDATA%\Claude` (also the MSIX location) |
| macOS | `~/Library/Application Support/Claude` |

Set `CLAUDE_USER_DATA` to point somewhere else. Transcripts are found under `~/.claude/projects`, or `CLAUDE_CONFIG_DIR` if you set it.

ccswitch keeps its own data (account names, backups, lock file) here:

| OS | Folder |
|---|---|
| Linux | `~/.config/ccswitch` |
| Windows | `%APPDATA%\ccswitch` |
| macOS | `~/Library/Application Support/ccswitch` |

## Safety

When you move a chat, ccswitch compares it with the same chat in the destination and puts it in one of these classes.

| Class | What it means | Default action |
|---|---|---|
| Missing | The destination does not have this chat. | Add it. |
| Identical | Both sides are the same. | Skip. |
| SourceNewer | The source was active more recently. | Merge source fields onto the destination. |
| DestNewer | The destination was active more recently. | Keep the destination. Shown as a conflict. |
| MetaOnly | Same activity. Only destination-owned settings differ. | Skip. |
| Orphan | The transcript file for the source chat is missing. | Skip with a warning. |
| DestOnly | The chat exists only in the destination. | Never touched. |

More guarantees:

- **Backups.** Every file that would be overwritten is copied to the backup folder first, with a manifest. The last 20 backup sets are kept.
- **Undo.** `ccswitch undo` restores the latest set. `--set` picks an older one.
- **Dry run.** `--dry-run` shows the plan and writes nothing. The Review screen in the TUI is a dry run too.
- **Atomic writes.** Each file is written to a temp file and renamed. After writing, ccswitch re-reads the file. If it does not parse, the backup is restored.
- **No deletes, ever.** ccswitch never deletes chats or transcripts. The only thing it removes is its own oldest backup sets, once there are more than 20.
- **Account settings stay put.** Destination-owned fields such as connector settings and last-focused time are kept. Unknown fields are kept.
- **No telemetry, and almost no network.** ccswitch reads and writes local files. The only network use is the optional once-a-day version check and the `ccswitch update` download, both from GitHub and described in [Updating](#updating). Downloads are verified against a SHA-256 checksum. ccswitch never starts or stops Claude Desktop.

## Troubleshooting

**"Store not found."** Run `ccswitch doctor`. It lists every folder it tried and why. If Claude Desktop keeps data somewhere unusual, set `CLAUDE_USER_DATA`:

```sh
export CLAUDE_USER_DATA="$HOME/path/to/Claude"
```

```powershell
$env:CLAUDE_USER_DATA = "D:\Apps\Claude"
```

**Chats do not show up after sync.** Claude Desktop reads the index when you switch account or start. Switch to the destination account, or restart Claude Desktop. ccswitch never closes it for you.

**Windows says a file is in use.** Claude Desktop may be holding it. ccswitch retries a few times, then marks that row as failed and goes on. Exit code is `3`. Close Claude Desktop and run the command again.

**Flatpak or snap.** ccswitch looks in `~/.var/app/*/config/Claude` and `~/snap/*/current/.config/Claude`. If your install is elsewhere, use `CLAUDE_USER_DATA`.

**An account shows as `account 0688fb6b...`.** ccswitch could not find a name. Give it one with `ccswitch label`.

## FAQ

**Is this safe?**
It is built to be. It backs up before every write, shows a dry run first, never deletes, and has `undo`. The code is small and MIT licensed, so you can read it.

**Does it move my transcripts?**
No. Transcripts are shared on your machine already. Only the small sidebar entries are copied.

**Does it work across machines?**
Not yet. Export and import bundles are planned for v0.2.

**Does it touch my account or tokens?**
No. It never reads or writes tokens, cookies or credentials. See the next question for the network.

**Does it touch the network?**
Only for updates. Once a day, at most, an interactive run asks GitHub for the latest release number (no data about you is sent beyond a `ccswitch/<version>` User-Agent), and `ccswitch update` downloads a release from GitHub. Turn the check off with `ccswitch update --disable-check` or `CCSWITCH_NO_UPDATE_CHECK=1`. It does not run in CI, in pipes, or in development builds. Nothing else uses the network, and there is no telemetry.

**Will a Claude Desktop update break it?**
Unknown fields are preserved, so new app versions should keep working. If something changes, `ccswitch doctor` shows what it found. Please open an issue with that output.

## Contributing

Bug reports and pull requests are welcome. Please run `ccswitch doctor` and paste the output into bug reports. Read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) first, then:

```sh
make lint
make test
```

Use Conventional Commit messages (`feat:`, `fix:`, `docs:`). Everything that writes needs a `--dry-run` branch. Do not add network calls beyond the update check and `ccswitch update`. See also [SECURITY.md](SECURITY.md) and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## Licence

[MIT](LICENSE). Copyright 2026 ccswitch contributors.

ccswitch is not affiliated with or endorsed by Anthropic. Claude and Claude Code are trademarks of Anthropic.
