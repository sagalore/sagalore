# Architecture

Sagalore is three programs that share one product name.

## Addon (Lua, in-process with WoW)

- No HTTP. No sockets. No Battle.net secret.
- Tracks objectives, writes `[Sagalore]` chat lines and per-character SavedVariables.
- Reads `SagaloreData.lua` (and related generated files) that the companion refreshes from the API.
- Slash commands `/sagalore` and `/sl` open the log UI.

## Companion (Go desktop app)

- Finds the WoW install, installs/updates the addon folder, and tails the chat log.
- Signs the player in with Battle.net via the API (browser redirect; secret stays on the server).
- Posts completions, levels, invites, and characters; pulls sync payloads and writes Lua data files.
- Optional tray UI and auto-update from GitHub Releases (`update.repo`, default `sagalore/sagalore`).

## API (Go + Postgres)

- Auth, groups, sagas, sync, leaderboards, group webhooks (JSON), OAuth for external clients.
- Realtime: Postgres `NOTIFY` on `sagalore_events`, one dedicated `LISTEN` connection per API process, WebSocket fan-out to companions.
- Hosted convenience at `api.sagalore.app`; self-host with Compose or any Postgres-backed host. Docs stay host-provider agnostic.
- Marketing site is a separate repo ([sagalore/website](https://github.com/sagalore/website)); the API does not serve it.

## Trust boundaries

| Claim | Reality |
| --- | --- |
| Completions / finish times | Client-reported. Honor-based. |
| Character ownership | First Battle.net account to link a name+realm wins; BattleTag on the character line must match when present. |
| Cover images | Signed URLs (`IMAGE_URL_SECRET`); not a CDN ACL. |
| Addon marketplace installs | Addon alone does nothing useful without the companion + an API. |

## Why the companion exists

WoW addons cannot safely hold OAuth secrets or talk to arbitrary HTTPS APIs in a way that fits this product. Keeping network and auth on a desktop companion is the ToS and security posture: the game process stays offline from Sagalore’s servers.
