# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Single static binary for Linux, Windows and macOS (amd64 and arm64).
- Guided terminal UI: Accounts, Direction, Chat picker, Review, Apply, Done.
- Per-chat selection, with filter, select all/none and project grouping.
- Conflict detection with seven classes: Missing, Identical, SourceNewer,
  DestNewer, MetaOnly, Orphan and DestOnly.
- Field-level merge. Destination-owned and unknown keys are preserved.
- Conflict policies (`keep-dest`, `prefer-source`, `ask`, `skip`) and modes
  (`merge`, `overwrite`, `add-only`).
- Backup before every write, with a manifest, and `ccswitch undo`.
  The last 20 backup sets are kept.
- Atomic writes (temp file and rename), post-write verification, and
  retry with backoff on Windows file locks.
- `--dry-run` on every command that writes.
- Commands: `list`, `chats`, `sync`, `unarchive`, `label`, `undo`, `doctor`,
  `version`, `completion`.
- Store discovery for Linux (including flatpak and snap), Windows (including
  MSIX) and macOS, with `CLAUDE_USER_DATA` override.
- Account labels from your own names, the app config, and the app log.
- Transcript check: chats whose transcript is missing are skipped by default.
- Lock file so two runs cannot overlap.
- ASCII fallback for glyphs and `NO_COLOR` support.
- Release tooling: GoReleaser, install scripts, Homebrew, Scoop, winget, AUR,
  deb, rpm and apk packages.
- No network access and no telemetry.

[Unreleased]: https://github.com/web-pruthvi-sigmasolve/ccswitch/commits/main
