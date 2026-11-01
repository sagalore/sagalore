# Changelog

## Open-source cleanup

Go module and update feed are `github.com/sagalore/sagalore` / `sagalore/sagalore`. Environment variables are `SAGALORE_*` only. Release assets are `sagalore-*` and `Sagalore-addon.zip`. Realtime Postgres notify uses the `sagalore_events` channel. The marketing site lives in [sagalore/website](https://github.com/sagalore/website); this repo no longer serves a landing page or ships host-vendor config.

## Leaderboards

Group members can read `GET /v1/groups/{id}/leaderboard?period=all|month|week|today`. The rank is how many distinct sagas a character has completed in that Oslo calendar window. There are no points. `GET /v1/sync` includes the same boards so the addon can show them. Any member of the saga's group can read `GET /v1/sagas/{id}`. Saga activity accepts `sort=completion_time` (fastest first) or `sort=completed_at` (newest first) and includes `duration_sec`. The addon records `started_at` when the first goal gains progress. Repeatable sagas are not enabled; the progress table can already store more than one finish per character and saga.

## OAuth 2.1 authorization server

External clients can register, send the player through Battle.net and a consent page, and call the API with a `gst_` access token. Refresh tokens rotate. Reusing an old refresh token revokes that client. The consent cookie is a short-lived sign-in for that page, not an API session. See [docs/oauth.md](docs/oauth.md).

## Group invites

Groups gained `GET /v1/groups/{id}/invites?status=pending` (owner or member), `DELETE /v1/groups/{id}/invites/{inviteId}` (owner), and `GET /v1/me/invites`.
