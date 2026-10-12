# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.2.0] - 2026-10-13

### Added

- Optional update check: at most once every 24 hours, an interactive run asks
  GitHub for the latest release and prints a one-line notice on stderr. It sends
  only a `ccswitch/<version>` User-Agent, never runs in CI, pipes, `--json`,
  `--quiet` or dev builds, and can be turned off with
  `ccswitch update --disable-check`, `CCSWITCH_NO_UPDATE_CHECK=1` or
  `--no-update-check`.
- `ccswitch update` (`--check`, `--yes`, `--version`, `--enable-check`,
  `--disable-check`): downloads the release, verifies its SHA-256 checksum and
  replaces the binary, or prints the right command for Homebrew, Scoop, winget,
  Linux packages and `go install`.
- `ccswitch version` shows the last known latest version; `ccswitch doctor`
  shows the state of the update check.
- The TUI shows the same notice as one line above the help bar; `u` opens the
  release details.

## [0.1.1] - 2026-10-13

### Fixed

- Release pipeline: Homebrew, Scoop and winget steps now skip themselves when
  their secrets are not set instead of failing the release.
- CI: tests run on current stable Go so macOS test binaries load (Go 1.22
  omitted LC_UUID); Go 1.22 remains the supported minimum.
- CI: line endings are forced to LF so the Windows gofmt check passes.

## [0.1.0] - 2026-10-12

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

[Unreleased]: https://github.com/web-pruthvi-sigmasolve/ccswitch/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/web-pruthvi-sigmasolve/ccswitch/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/web-pruthvi-sigmasolve/ccswitch/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/web-pruthvi-sigmasolve/ccswitch/releases/tag/v0.1.0
