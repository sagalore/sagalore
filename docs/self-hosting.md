# Self-hosting Sagalore

Run your own API + Postgres. Point companions at that API. No particular cloud vendor is required or assumed.

This page is intentionally short: enough for a human or an LLM to fork and stand something up. Deep HTTP shapes live in [API.md](API.md), [oauth.md](oauth.md), and [sagas-api.md](sagas-api.md).

## Minimum path

1. **Postgres** reachable with a connection string (`DATABASE_URL`).
2. **API** from this repo: `docker compose up --build -d` locally, or build `api/Dockerfile` (context = repo root) and run the container / `go run ./api`.
3. **Secrets** (production): real `ADMIN_TOKEN`, `IMAGE_URL_SECRET` (`openssl rand -hex 32`). Compose defaults are rejected unless `SAGALORE_ALLOW_DEV_SECRETS=1`.
4. **Battle.net** app for that host: `BNET_CLIENT_ID`, `BNET_CLIENT_SECRET`, `PUBLIC_BASE_URL` (no trailing slash). Redirect URI = `{PUBLIC_BASE_URL}/v1/auth/bnet/callback`.
5. **Companion** against your API: `go run ./cmd/sagalore --api-url https://your.api.example` (or a release binary with `--api-url`). Set `update.repo` to your fork, or disable updates, if you are not using official builds.

Dev shortcut (no Battle.net): `make dev`, then `SAGALORE_DEV_TOKEN=dev-token SAGALORE_ADDON_SRC=addon/Sagalore go run ./cmd/sagalore --api-url http://localhost:8080 --client classic_era`. The API must see the same `SAGALORE_DEV_TOKEN`.

## Compose

```bash
docker compose up --build -d
```

Listens on port 8080. Good for local development and a single host. Migrations run on API startup.

## Environment

| Variable | Required | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | yes | Postgres connection string |
| `ADMIN_TOKEN` | for ops routes | Hard-delete a saga, or delete a group and its sagas. Cannot author sagas. |
| `BNET_CLIENT_ID` | for login | Battle.net OAuth client id |
| `BNET_CLIENT_SECRET` | for login | Stays on the API. Never put it in the companion. |
| `PUBLIC_BASE_URL` | for login and covers | Public origin, no trailing slash. Redirect URI is `{PUBLIC_BASE_URL}/v1/auth/bnet/callback`. |
| `IMAGE_URL_SECRET` | for signed covers | `openssl rand -hex 32` |
| `SAGALORE_BOOTSTRAP_GROUP` | when old sagas have no group | `slug:Name`. A fresh database does nothing. |
| `SAGALORE_OWNER_BATTLETAG` | no | Owner of that bootstrap group. Otherwise the oldest player who has a BattleTag. |
| `SAGALORE_BOOTSTRAP_WEBHOOK_URL` | no | Copied once onto the bootstrap group when its webhook is empty. After that the group row is the source of truth. New groups stay quiet until the owner sets a webhook (`PUT /v1/groups/{id}/webhook`). See [webhooks.md](webhooks.md). |
| `SAGALORE_DEV_TOKEN` | dev and CI only | Login bypass. It is not a stand-in for Battle.net. |
| `SAGALORE_CREATE_URL` | no | Optional override for the companion “Create sagas…” link (default is the official site). |

Each host registers its own Battle.net application and redirect URI. WoW Forever characters are not in Blizzard's profile API. The addon reports characters. Login does not wait for profile verification.

The same Battle.net app and redirect URI also sign in players for the OAuth 2.1 authorization server. External clients register at `POST /oauth/register`. They do not get the Battle.net client secret. See [oauth.md](oauth.md). Discovery: `/.well-known/oauth-authorization-server` and `/.well-known/oauth-protected-resource`.

## Companion

```bash
go run ./cmd/sagalore --api-url https://sagalore.example.com
```

Official release binaries bake the public URL with:

```bash
-X github.com/sagalore/sagalore/internal/version.DefaultAPIBaseURL=${SAGALORE_API_URL}
```

`SAGALORE_API_URL` is a GitHub Actions variable on the official repository. If it is empty, the companion requires `--api-url` or `SAGALORE_API_URL`.

Module path: `github.com/sagalore/sagalore`. Default update feed: `sagalore/sagalore` on GitHub Releases.

## Create a group and a saga

Use a `gst_` token from `POST /v1/me/tokens` (shown once):

```bash
curl -s -X POST "$API/v1/groups" \
  -H "Authorization: Bearer $GST" -H "Content-Type: application/json" \
  -d '{"slug":"tutorial","name":"Tutorial","locale":"en"}'

curl -s -X POST "$API/v1/sagas" \
  -H "Authorization: Bearer $GST" -H "Content-Type: application/json" \
  -d '{"title":"An evening in Goldshire","description":"Visit the inn.","party":true,"group":"tutorial","objectives":[{"id":"inn","type":"go","text":"Stand in Goldshire","zone":"Elwynn Forest"}]}'
```

If the player is in exactly one group, omit `group`.
