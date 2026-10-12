# Architecture

A short guide for contributors. The full spec is in `PLAN.md`.

## Goals

- One static Go binary for Linux, Windows and macOS.
- Two front-ends (TUI and CLI) over one core.
- The risky logic (planning, merging) is pure and easy to test.
- No network, no telemetry. Backup before every write.

## Package map

```
cmd/ccswitch/main.go   entrypoint: TTY and no args -> TUI, else CLI
internal/
  store/      find the user-data folder, list accounts -> buckets -> entries,
              locate transcripts (stat only, never read)
  identity/   human labels for account UUIDs
  plan/       PURE. Source bucket + destination bucket + selection + policy
              -> Plan{Ops}, each with a conflict class. No IO.
  merge/      PURE. Field-level merge of a source entry onto a destination entry.
  exec/       Apply a Plan: backup, atomic write, verify. Windows lock retry.
  backup/     Timestamped backup sets with manifest. Restore for undo.
  config/     ccswitch's own folder: labels, preferences, backups, lock file.
  cli/        cobra commands
  tui/        Bubble Tea screens
  version/    Version, Commit, Date set by ldflags
testdata/     fixture stores (Linux-shaped and Windows-shaped)
```

Dependency rule: `tui` and `cli` may import `exec`, `plan`, `merge`, `store`, `identity`.
`plan` and `merge` import only the domain model. Never import `tui` or `cli` from the core.

## Data flow

```
store.Discover  ->  Store (accounts, buckets, entries)
identity.Label  ->  names for accounts
user picks source, destination, chats, policy
plan.Build(src, dst, selection, policy)  ->  Plan   (pure)
          |
          +-- dry run / Review screen: print the Plan, stop
          |
exec.Apply(Plan):
   1. lock file
   2. backup every file that will be overwritten (+ manifest.json)
   3. merge.Apply or byte copy -> <file>.ccswitch-tmp
   4. rename over the target (retry on Windows)
   5. re-read, check sessionId parses, else restore from backup
   6. prune old backup sets (keep 20)
```

## Domain model

An `Entry` is one `local_<id>.json` file: `SessionID`, `CLISessionID`, `Title`,
`Cwd`, `OriginCwd`, timestamps (`CreatedAt`, `LastActivityAt`, `LastFocusedAt`, ms epoch),
`IsArchived`, the raw bytes, file mode, and the transcript path with an exists flag.
A `Bucket` is one `<account>/<org>` folder of entries. An `Account` has one or more buckets.

Transcript path: `~/.claude/projects/<encode(cwd)>/<cliSessionId>.jsonl`.
`encode` turns path separators, `:` and spaces into `-`. If the direct lookup misses,
glob `~/.claude/projects/*/<cliSessionId>.jsonl`.

## Conflict classes

For each selected source entry, compare with the destination entry that has the same `sessionId`.

| Class | Condition | Default action |
|---|---|---|
| `Missing` | no file at destination | Add |
| `Identical` | bytes equal | Skip |
| `SourceNewer` | src `lastActivityAt` greater, or equal and src `lastFocusedAt` >= dst | Merge |
| `DestNewer` | dst `lastActivityAt` greater | Keep destination, show as conflict |
| `MetaOnly` | same activity, differs only in destination-owned keys | Skip |
| `Orphan` | source transcript not found | Skip and warn (`--include-orphans` to override) |
| `DestOnly` | only at destination | Never touched, never listed as a change |

Policies (`--on-conflict`): `keep-dest` (default), `prefer-source`, `ask` (TUI), `skip`.
Modes (`--mode`): `merge` (default), `overwrite`, `add-only`.

## Merge policy

| Kind | Keys | On merge |
|---|---|---|
| Source-owned | `title`, `titleSource`, `titleTurn`, `isArchived`, `lastActivityAt`, `completedTurns`, `lastAssistantUuid`, `postTurnSummary`, `postTurnSummaryFor`, `latestUserFrameAt`, `model`, `effort`, `permissionMode`, `cwd`, `originCwd`, `gitAnchors` | Copied from source |
| Destination-owned | `lastFocusedAt`, `remoteMcpServersConfig`, `terminalClaudeTabOrdinal`, `sessionPermissionUpdates`, `alwaysAllowedReasons` | Kept from destination |
| Unknown, in both | any other key | Kept from destination |
| Unknown, source only | any other key | Added (forward compatibility) |

On **Add**, the source bytes are copied as they are. With `--strip-account-config` (default on),
`remoteMcpServersConfig` is replaced with `[]`. Merged output is minified single-line JSON with
no trailing newline and no BOM, like the app writes. File mode comes from the destination
bucket's existing files, else the source file.

## Safety rules for code

1. Every write path has a `--dry-run` branch.
2. Writes are temp file then rename. Never write in place.
3. Back up before overwriting. Never delete user files.
4. Unreadable entries are listed as unreadable and never copied or merged.
5. Only `local_*.json` files are touched. Never `scheduled-tasks.json`.
6. Never print or log tokens, emails or file contents.
7. No network calls. A lock file prevents two runs at once.

## Adding a screen to the TUI

Each screen is its own Bubble Tea model in `internal/tui`. The app model owns a current screen
and switches between them with messages.

1. Create `internal/tui/<name>.go` with a model that implements `Init`, `Update` and `View`
   (copy the shape of an existing screen such as the chat picker).
2. Keep it free of IO. Take what it needs (accounts, a `plan.Plan`) in its constructor.
3. Define the messages it sends to move on or go back (for example `nextMsg{...}`, `backMsg{}`).
4. Register it in the app model: add a case in `Update` for the message that opens it, and a
   case in `View` to render it.
5. Wire the keys: `enter` next, `esc` back, `q` quit. Add the key hints to the footer.
6. Provide ASCII fallbacks for any glyphs, and respect `NO_COLOR`.
7. Test `Update` with table tests that feed key messages and check the next state.
8. If the screen writes anything, route it through `exec.Apply` and show it on the Review screen first.
9. Update `demo.tape` and the README Usage section if the flow changes.

## Build and release

- `make build`, `make test`, `make lint`, `make cross`, `make snapshot`, `make demo`.
- Tagging `vX.Y.Z` runs `.github/workflows/release.yml`, which runs GoReleaser
  (`.goreleaser.yaml`). It builds six targets, writes `checksums.txt`, and publishes
  archives, deb/rpm/apk, Homebrew, Scoop, winget and AUR.
- `internal/version` receives `Version`, `Commit` and `Date` through ldflags.
