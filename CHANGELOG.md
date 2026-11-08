# Changelog

## Group webhook enrichment

Saga webhook events include structured `objectives` and optional signed `image_url`. Groups may store opaque `webhook_meta` (JSON) via `PUT /v1/groups/{id}/webhook` (`meta`); it is copied onto each event. Owners can `GET /v1/groups/{id}/webhook`. See [docs/webhooks.md](docs/webhooks.md). The Smia Sagalore MCP adds `webhook_guide`, `set_group_webhook`, Discord helpers, and a Discord format relay.

## Join group links

Owners can mint time-limited shareable join URLs (`open` or `gated`) via `POST /v1/groups/{id}/join-links`. Redeem is `POST /v1/join/{token}` (JSON) or the thin browser page at `/join/{token}` (Battle.net sign-in + character checkboxes from companion sync). After join (and on empty roster) the page links **Install Sagalore** to `SAGALORE_SITE_URL` (default `https://sagalore.app`). Gated requests need owner approve/reject. Personal groups stay link-free. Group webhooks get `join.joined`, `join.requested`, `join.approved`, and `join.rejected`. See [docs/API.md](docs/API.md) and [docs/webhooks.md](docs/webhooks.md).

## Open-source cleanup

Go module and update feed are `github.com/sagalore/sagalore` / `sagalore/sagalore`. Environment variables are `SAGALORE_*` only. Release assets are `sagalore-*` and `Sagalore-addon.zip`. Realtime Postgres notify uses the `sagalore_events` channel. The marketing site lives in [sagalore/website](https://github.com/sagalore/website); this repo no longer serves a landing page or ships host-vendor config.

## Leaderboards

Group members can read `GET /v1/groups/{id}/leaderboard?period=all|month|week|today`. The rank is how many distinct sagas a character has completed in that Oslo calendar window. There are no points. `GET /v1/sync` includes the same boards so the addon can show them. Any member of the saga's group can read `GET /v1/sagas/{id}`. Saga activity accepts `sort=completion_time` (fastest first) or `sort=completed_at` (newest first) and includes `duration_sec`. The addon records `started_at` when the first goal gains progress. Repeatable sagas are not enabled; the progress table can already store more than one finish per character and saga.

## OAuth 2.1 authorization server

External clients can register, send the player through Battle.net and a consent page, and call the API with a `gst_` access token. Refresh tokens rotate. Reusing an old refresh token revokes that client. The consent cookie is a short-lived sign-in for that page, not an API session. See [docs/oauth.md](docs/oauth.md).

## Group invites

Groups gained `GET /v1/groups/{id}/invites?status=pending` (owner or member), `DELETE /v1/groups/{id}/invites/{inviteId}` (owner), and `GET /v1/me/invites`.
