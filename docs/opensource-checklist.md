# Open-source publish checklist

Use this when moving from the private archive to the public `sagalore/sagalore` repo.

## Already done in-tree

- [x] Go module `github.com/sagalore/sagalore`
- [x] `DefaultUpdateRepo` = `sagalore/sagalore`
- [x] `SAGALORE_*` env only (no dual-read fallbacks)
- [x] Single release asset names (`sagalore-*`, `Sagalore-addon.zip`)
- [x] English CI release download notes
- [x] Honest README (Cursor + AI assets, Mac-first matrix, unsigned, honor-based times)
- [x] `docs/architecture.md`
- [x] Realtime channel / triggers named `sagalore_*` (see migration `016` for existing DBs)
- [x] Marketing site extracted to [sagalore/website](https://github.com/sagalore/website); no host-vendor config in the product repo

## Before the first public release

- [x] Create org repo `sagalore/sagalore` (private until ready)
- [x] Seed with a **clean initial commit** (or short curated series); keep the old private repo as archive
- [x] Set Actions variable `SAGALORE_API_URL` on the new repo
- [x] Apply pending API migrations on the hosted database (especially `016_realtime_sagalore_channel.sql`) before flipping companion builds that `LISTEN` on `sagalore_events`
- [ ] Branch protection on `main` + required CI (needs GitHub Pro on a private org repo, or make the repo public)
- [x] Smoke release `v0.1.0` on `sagalore/sagalore`: all OS assets download; `https://api.sagalore.app` baked into companions; addon zip lists `Sagalore/` (full in-game install path not re-tested here)
- [x] Marketing on separate host ([sagalore/website](https://github.com/sagalore/website)); API hostname separate
- [x] Confirm Discord OG still uses `https://sagalore.app/assets/og-image.jpg`
- [x] Grep seed tree: no host-vendor names; MCP platform named only in README honesty bullet (product deep links use sagalore.app)

## Release workflow (tighten when public)

- [x] Prefer tag `v*` or explicit dispatch over “every push to main ships”
- [ ] Ensure the workflow creates/pushes the annotated tag that `next-version.sh` expects
- [ ] Keep artifact contract documented in README (companion auto-update depends on exact names)
- [x] `contents: write` only on the release job; PR job read-only
- [ ] Dependabot for Go modules (optional, lightweight)

## Addon catalog automation (later)

Not required for GitHub Releases. Companion users get `Sagalore-addon.zip` from GitHub.

- [ ] CurseForge / Wago project ids + “requires companion” copy
- [ ] Sync TOC `## Version` with the release tag in CI
- [ ] BigWigs packager (or equivalent) upload on release publish
- [ ] Same version number on store and GitHub

## Do not rush

- macOS / Windows code signing
- Linux companion
- Drive-by feature PRs without an issue
- Auto-shipping every `main` commit once the repo is watched
