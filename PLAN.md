# ccswitch — product and architecture plan

> Switch Claude Desktop accounts without losing your Claude Code chats.
> A single-binary, cross-platform tool (Linux, Windows, macOS) with a guided
> terminal UI and a scriptable CLI.

Status: v0.1 built on 2026-10-12 (all work packages in §7 done; Linux verified
end to end, Windows and macOS cross-compiled only). Supersedes the bash
prototype (`legacy/switch.sh`).

---

## 1. Problem

Claude Desktop keeps a **per-account sidebar index** of Claude Code chats:

```
<userData>/claude-code-sessions/<account-uuid>/<org-uuid>/local_<id>.json
<userData>/claude-code-sessions/<account-uuid>/<org-uuid>/scheduled-tasks.json
```

The **transcripts** themselves are account-agnostic and shared on the machine:

```
~/.claude/projects/<cwd-encoded>/<cliSessionId>.jsonl     (+ a sibling dir)
```

So when a user signs out of account A and into account B, every chat
"disappears" from the sidebar even though the data is still on disk. Making a
chat visible in B is a matter of placing its small index entry into B's bucket.

The bash prototype proves this works. It is not shippable because:

| Gap | Why it matters at scale |
|---|---|
| Copies **all** chats; no per-chat selection | Users want to move 3 chats, not 300 |
| Blindly **overwrites** entries that already exist at the destination | Destroys the destination's newer title / archive state / focus time. Observed on this very machine: one chat exists in both accounts with different `lastFocusedAt` and `remoteMcpServersConfig` |
| No backup, no undo | One wrong keypress is unrecoverable |
| No transcript check | A copied entry whose `.jsonl` is gone becomes a dead sidebar row |
| bash only; separate `.ps1` needed for Windows | Two codebases, two sets of bugs |
| Account identity is best-effort (log + IndexedDB scrape) | Users can't tell "account 0688fb6b…" from "account e332681c…" |
| Copies account-specific fields verbatim (`remoteMcpServersConfig`) | Connectors are account-scoped; copied config may point at connectors the destination account doesn't have |

---

## 2. Product decisions

**Name**: `ccswitch` (Claude Code switch). Follows the `cc*` convention the
community already knows (`ccusage` etc.). Module path placeholder
`github.com/web-pruthvi-sigmasolve/ccswitch`; rename with one `sed` once the GitHub org exists.

**Language**: **Go 1.22+**.
Why Go over Rust/TypeScript/Python for this product:

- One static binary per OS/arch, zero runtime deps. Users on Windows and Linux
  download one file. No Python, no Node, no `jq`.
- Trivial cross-compilation (`GOOS=windows go build`) and a mature release
  pipeline (GoReleaser → GitHub Releases, Homebrew, Scoop, winget, AUR, deb/rpm).
- Bubble Tea gives a composable Elm-architecture TUI (model → update → view)
  that renders well in Windows Terminal, cmd.exe (with ANSI), and every Linux
  terminal. Each screen is an independent model composed into the app model.
- Fast enough to scan thousands of JSON files in well under a second.
- Rust would be equally good on safety/perf but ~2× slower to iterate on UI;
  TypeScript needs a runtime or a 80 MB `pkg` blob; Python needs an interpreter
  on Windows.

**Two front-ends, one core**:

- `ccswitch` with no args in a TTY → guided TUI.
- `ccswitch <command> …` → non-interactive CLI for scripts, CI, and users who
  prefer flags. Also what runs when stdout is not a TTY.

**No telemetry; one optional network call.** Reads/writes only the local
Claude data folders and its own config/backup folder. The single exception is
the once-a-day version check against GitHub Releases (added after v0.1.1): no
identifying data, 3-second timeout, background only, never in CI or pipes,
off with one setting. `ccswitch update` downloads release archives from
GitHub and verifies their SHA-256. Both are stated in the README and
SECURITY.md; trust is the moat.

---

## 3. Architecture

```
cmd/ccswitch/main.go            entrypoint: TTY + no args → TUI, else CLI
internal/
  store/        discover user-data folder (per OS, env override, flatpak/snap/
                portable), enumerate accounts → orgs (buckets) → entries;
                parse only the fields we need, keep raw bytes; locate transcripts
  identity/     human labels for account UUIDs: user labels (config), app
                config.json `lastKnownAccountUuid` (= current account), main.log
                "Identity changed" lines, IndexedDB/LevelDB profile scrape (best
                effort, never required)
  plan/         PURE: given src bucket, dst bucket, selection, policy →
                Plan{Ops} with per-entry Conflict classification. No IO.
  merge/        PURE: field-level merge of a source entry onto a destination
                entry (map[string]json.RawMessage), preserving unknown and
                destination-owned keys
  exec/         apply a Plan: backup → atomic write (temp + rename, keep mode
                0600) → verify; Windows file-lock retry with backoff
  backup/       timestamped backup sets with manifest; `undo` restores last set
  config/       ~/.config/ccswitch (Linux), %APPDATA%\ccswitch (Windows),
                ~/Library/Application Support/ccswitch (macOS): labels,
                preferences, backup retention
  cli/          cobra commands (list, sync, unarchive, undo, doctor, version,
                completion; later export/import)
  tui/          bubbletea screens: Accounts → Direction → Chat picker →
                Review → Apply → Done. Each screen is its own model.
  version/      ldflags-injected version/commit/date
testdata/       fixture stores (Linux and Windows shaped) for tests
```

Dependency direction: `tui` and `cli` → `exec`/`plan`/`merge`/`store`/`identity`.
`plan` and `merge` import nothing but the model. This keeps the risky logic
unit-testable without touching a filesystem.

### 3.1 Domain model

```go
type Store    { UserData, Root string; Accounts []Account }
type Account  { UUID string; Buckets []Bucket; Label Label }
type Bucket   { AccountUUID, OrgUUID, Path string; Entries []Entry }
type Entry    {
  SessionID, CLISessionID, Title, Cwd, OriginCwd string
  CreatedAt, LastActivityAt, LastFocusedAt int64   // ms epoch
  IsArchived bool
  Path string; Raw []byte; ModTime time.Time; Mode fs.FileMode
  TranscriptPath string; TranscriptExists bool
}
```

Transcript location: `~/.claude/projects/<encode(cwd)>/<cliSessionId>.jsonl`
where `encode` replaces every path separator, `:` and space with `-`
(observed: `/var/www/X` → `-var-www-X`; Windows `C:\Users\me\x` → `C--Users-me-x`).
Treat this as a heuristic: if the direct lookup misses, fall back to a glob over
`~/.claude/projects/*/<cliSessionId>.jsonl`.

### 3.2 Conflict classification (the core safety feature)

For each selected source entry, compare with the destination entry of the same
`sessionId`:

| Class | Condition | Default action |
|---|---|---|
| `Missing` | no file at destination | **Add** |
| `Identical` | bytes equal | Skip |
| `SourceNewer` | src `lastActivityAt` > dst, or equal activity and src `lastFocusedAt` ≥ dst | **Merge** (src fields onto dst) |
| `DestNewer` | dst `lastActivityAt` > src | **Keep destination** (skip), show as conflict |
| `MetaOnly` | same activity, differs only in dest-owned keys | Skip |
| `Orphan` | source transcript file not found | Skip, warn (opt-in `--include-orphans`) |
| `DestOnly` | exists only at destination | Never touched, never listed as a change |

Policies (`--on-conflict`): `keep-dest` (default), `prefer-source`, `ask`
(TUI per-row choice), `skip`.

Modes (`--mode`): `merge` (default), `overwrite` (byte copy, old behaviour),
`add-only` (never touch existing).

### 3.3 Field-level merge

Source-owned keys (copied on merge): `title`, `titleSource`, `titleTurn`,
`isArchived`, `lastActivityAt`, `completedTurns`, `lastAssistantUuid`,
`postTurnSummary`, `postTurnSummaryFor`, `latestUserFrameAt`, `model`,
`effort`, `permissionMode`, `cwd`, `originCwd`, `gitAnchors`.

Destination-owned keys (kept): `lastFocusedAt`, `remoteMcpServersConfig`,
`terminalClaudeTabOrdinal`, `sessionPermissionUpdates`, `alwaysAllowedReasons`
and **every key we don't recognise**. Unknown keys present only in the source
are added (forward compatibility with new app versions).

On **Add** (no destination file) the source bytes are copied verbatim, except
`remoteMcpServersConfig` is replaced with `[]` when `--strip-account-config`
is set (default on; the app repopulates it on next open).

Output is minified single-line JSON with no trailing newline, matching what the
app writes. File mode copied from the destination bucket's existing files
(0600 observed) or the source file.

### 3.4 Safety and atomicity

1. Every file that would be overwritten is copied to
   `<config>/backups/<RFC3339>/<account>/<org>/<file>` and listed in
   `manifest.json` together with the plan that produced it.
2. Writes go to `<file>.ccswitch-tmp` then `rename`. On Windows, rename over an
   open file can fail with sharing violations; retry 5× with backoff, then
   report that row as failed and continue (never abort mid-plan).
3. After writing, re-read and verify `sessionId` parses; otherwise restore from
   backup immediately.
4. `ccswitch undo` restores the latest backup set (or `--set <timestamp>`).
   Retention: keep the last 20 sets, prune on each run.
5. A lock file in the config dir prevents two concurrent `ccswitch` runs.
6. Detect a running Claude Desktop (process name `claude`, `Claude`,
   `claude-desktop`, `Claude.exe`) and show the "switch account or restart to
   see changes" note. Never kill it.
7. `--dry-run` everywhere. The TUI's Review screen *is* a dry run; nothing is
   written until the user confirms on that screen.

### 3.5 Store discovery

| OS | Candidates (in order) |
|---|---|
| Linux | `$CLAUDE_USER_DATA`, `~/.config/Claude`, `~/.var/app/*/config/Claude` (flatpak), `~/snap/*/current/.config/Claude`, `$XDG_CONFIG_HOME/Claude` |
| Windows | `%CLAUDE_USER_DATA%`, `%APPDATA%\Claude`, `%LOCALAPPDATA%\Claude`, `%LOCALAPPDATA%\Packages\*Claude*\LocalCache\Roaming\Claude` (MSIX) |
| macOS | `$CLAUDE_USER_DATA`, `~/Library/Application Support/Claude`, `~/Library/Containers/*/Data/Library/Application Support/Claude` |

Pick the candidate with the most entries, tie-break on bucket count then
recency. `ccswitch doctor` prints every candidate and why it was/wasn't chosen.

Transcript root: `~/.claude/projects` (`%USERPROFILE%\.claude\projects`),
overridable with `CLAUDE_CONFIG_DIR` (same variable Claude Code honours).

### 3.6 Account identity

Label resolution order, first hit wins:

1. User label from `<config>/accounts.json` (`{"<uuid>": "work"}`).
2. `lastKnownAccountUuid` in `<userData>/config.json` → suffix "(current)".
3. Last `[account] Identity changed` line in `<userData>/logs/main.log`
   → suffix "(last signed in)".
4. LevelDB/IndexedDB profile scrape for `email_address` near the UUID
   (best effort; may yield nothing).
5. Fallback: `account 0688fb6b…` plus a fingerprint line: chat count, last
   active, top 3 project folders, 3 latest titles.

The TUI asks for a label on first sight of an unlabelled account (skippable).

### 3.7 TUI flow (Bubble Tea)

```
┌ Accounts ─────────────────────────┐   ┌ Pick chats ──────────────────────────┐
│ > work (current)  124 chats       │   │ filter: ______   [a]ll [n]one [p]roj │
│   personal        37 chats        │   │ ▸ ShopifyInforConnector (3)          │
│   account 9f3a…   2 chats         │ → │   [x] Orchestrator with Opus   ✓ new │
│                                   │   │   [ ] Fix pint failures      ⚠ dest  │
│ enter: choose source  l: label    │   │   [x] Add webhook retries    ↑ newer │
└───────────────────────────────────┘   └──────────────────────────────────────┘
          ↓                                         ↓
┌ Direction ────────────┐   ┌ Review ───────────────────────────────────────────┐
│ FROM work → TO ?      │   │ 2 add · 1 merge · 1 kept (dest newer) · 0 orphan  │
│ > personal            │   │ Backup → ~/.config/ccswitch/backups/2026-10-12…   │
│   account 9f3a…       │   │ [enter] apply   [c] conflict policy   [esc] back  │
└───────────────────────┘   └───────────────────────────────────────────────────┘
```

Keys: `↑↓/jk` move, `space` toggle, `a/n` all/none, `/` filter, `p` collapse
project, `c` cycle conflict policy for the row, `enter` next, `esc` back,
`q` quit. Row glyphs have ASCII fallbacks (`--ascii`, or auto when
`TERM=dumb` / legacy console).

### 3.8 CLI surface

```
ccswitch                                   # TUI
ccswitch list [--json]                     # accounts with labels + fingerprints
ccswitch chats --account <n|uuid|label> [--project X] [--since 7d] [--archived] [--json]
ccswitch sync --from work --to personal [--all | --project X | --since 7d | --id id…]
              [--mode merge|overwrite|add-only] [--on-conflict keep-dest|prefer-source|skip]
              [--include-orphans] [--dry-run] [--yes]
ccswitch unarchive --account work [--all | --id …] [--dry-run]
ccswitch label <uuid|n> "<name>"
ccswitch undo [--list | --set <ts>]
ccswitch doctor                            # paths, candidates, versions, permissions
ccswitch version
ccswitch completion bash|zsh|fish|powershell
```

Exit codes: 0 ok, 1 user error, 2 nothing to do, 3 partial failure, 4 store
not found.

---

## 4. Edge cases to handle (checklist)

Store / files
- [ ] 0 or 1 account bucket: explain, still allow `unarchive`/`list`.
- [ ] Same org UUID under two accounts (observed) vs different orgs: allow, warn
      when crossing orgs ("org-scoped connectors may not match").
- [ ] Non-UUID folders inside the store: ignore.
- [ ] `scheduled-tasks.json` and any non-`local_*.json` file: never touched.
- [ ] CRLF / BOM in entries (Windows): preserve on byte-copy; merge re-emits
      without BOM, matching the app.
- [ ] Entry that fails to parse: list as "unreadable", never copy or merge.
- [ ] Entry missing `sessionId` or whose `sessionId` ≠ filename: treat as
      unreadable.
- [ ] Transcript missing (orphan): skip by default.
- [ ] `cwd` no longer exists on disk (project moved/deleted): copy, mark "⚠ path missing".
- [ ] Thousands of entries: parse concurrently (bounded worker pool), stream the
      list, never read transcripts (only `stat` them).
- [ ] Read-only / locked destination file (Windows sharing violation): retry,
      then report per row, exit 3.
- [ ] Disk full mid-write: temp-file write fails before rename; nothing corrupted.
- [ ] Two `ccswitch` processes: lock file.
- [ ] Destination bucket directory doesn't exist yet (account signed in but
      never used Claude Code): create with 0700 (only if the account dir exists).

Identity
- [ ] Unlabelled accounts: fingerprint + prompt to label.
- [ ] Stale labels for accounts that no longer exist: keep but hide.
- [ ] Email scrape failing: silent.

UX / platform
- [ ] Not a TTY (piped, CI): never launch TUI; `sync` requires `--yes` or `--dry-run`.
- [ ] Windows legacy console without ANSI: ASCII glyphs, no colour
      (`NO_COLOR` honoured).
- [ ] Narrow terminals (< 80 cols): wrap titles, collapse columns.
- [ ] Claude Desktop running: note that the sidebar refreshes on account switch
      or restart. Never kill the app.
- [ ] App version drift: unknown JSON keys preserved; `doctor` prints the app
      version read from the newest entry's `toolSurfaceSnapshot.appVersion`.

---

## 5. Roadmap

**v0.1 (this build)** — Linux + Windows + macOS binary. TUI with chat
selection, conflict-aware merge, backup + undo, unarchive, labels, doctor,
dry-run, GoReleaser config, install scripts, README.

**v0.2** — `export` / `import` bundles (`.ccswitch.zip` with entries +
transcripts + path remap table) so chats move **between machines** and between
Windows ↔ Linux (path translation of `cwd` and the encoded project folder).
Scheduled "keep accounts in sync" mode (`ccswitch sync --watch`).

**v0.3** — "Claude Code session manager" positioning: search across all chats,
archive/bulk-archive, delete-with-backup, export a chat to Markdown, disk usage
report. Each is a separate screen composed into the same TUI.

**v1.0** — Signed Windows binary (SmartScreen), notarised macOS binary,
winget/Homebrew/Scoop/AUR packages, auto-update check (opt-in).

---

## 6. Path to a large user base (honest view)

Multi-account Claude Desktop users are a niche; "10M+" is a reach goal that
needs the broader **session manager** scope (v0.3), where the audience is every
Claude Code user. Concrete levers, cheapest first:

1. **README that sells in 10 seconds**: a 15-second terminal GIF (made with
   `vhs`), one-line install for each OS, and the privacy line "no telemetry,
   backup before every write, the only network use is an optional version
   check you can turn off".
2. **Install friction → zero**: `curl -fsSL …/install.sh | sh`,
   `irm …/install.ps1 | iex`, `brew install`, `scoop install`, `winget install`,
   `go install`.
3. **Distribution channels**: Hacker News "Show HN", r/ClaudeAI, X/Twitter
   dev community, Product Hunt, awesome-claude-code lists, the Claude Discord.
4. **Searchable problem statement**: title the repo for the query people type
   ("claude desktop switch account chats disappeared").
5. **Trust signals**: MIT licence, reproducible builds via GoReleaser, SHA256
   sums, signed releases, a CHANGELOG, issues template that asks for
   `ccswitch doctor` output.
6. **Expand the wedge**: every v0.3 feature targets a search query Claude Code
   users already make (search old chats, export chat, free disk space).

---

## 7. Build plan (work packages)

| # | Package | Depends on | Parallel group |
|---|---|---|---|
| 1 | Scaffold, `store`, `config`, `identity`, fixtures | — | A |
| 2 | `plan`, `merge`, `backup`, `exec` + unit tests | 1 | B |
| 3 | `cli` (cobra) wiring all commands | 1, 2 | C |
| 4 | `tui` (Bubble Tea) screens | 1, 2 | C |
| 5 | GoReleaser, CI, install scripts, README, CHANGELOG, licence | — | B |
| 6 | Integration test against a fixture store; dry-run against the real store | 3, 4 | D |

Coding rules for every package: `gofmt`, `go vet`, `staticcheck` clean; table
tests; no global state except `version`; errors wrapped with `%w` and shown to
users as one plain sentence plus a hint; every write path has a `--dry-run`
branch; never log or print tokens, emails, or file contents.
