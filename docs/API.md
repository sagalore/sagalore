# Sagalore HTTP API

Players sign in with Battle.net. The companion session is `gss_` and lasts 30 days. External clients use a personal token `gst_`. `ADMIN_TOKEN` is ops only.

The Go module path is still `github.com/sagalore/sagalore` until the public snapshot. Paths stay `/v1/sagas`.

## Login

`GET /v1/auth/bnet/start?attempt=` redirects to `https://oauth.battle.net/authorize` (authorization code, PKCE, scope `openid wow.profile`).

`GET /v1/auth/bnet/callback` exchanges the code with `BNET_CLIENT_ID` and `BNET_CLIENT_SECRET`, reads userinfo (`id`, `battletag`), merges or creates the player, and stores the session on the attempt.

`GET /v1/auth/bnet/poll?attempt=` returns the session when the browser flow finishes, or `202` while it is pending.

The redirect URI is `{PUBLIC_BASE_URL}/v1/auth/bnet/callback`. Each host registers its own Battle.net app.

`POST /v1/auth/dev` with `Authorization: Bearer $SAGALORE_DEV_TOKEN` issues a session without calling Blizzard. Dev and CI only.

Login merges an existing player by `bnet_account_id`, else by BattleTag. No other key.

## Tokens

| Method | Path | |
| --- | --- | --- |
| `POST` | `/v1/me/tokens` | Mint `gst_`. The raw token is in the response once. It does not expire. An OAuth access token cannot call this. |
| `DELETE` | `/v1/me/tokens/{id}` | Revoke. A session or hand-minted `gst_` can revoke an OAuth access token, which also revokes its refresh tokens. An OAuth access token cannot call this. |

External clients can also obtain a `gst_` access token from the OAuth 2.1 authorization server. That token expires. A refresh token (`gsr_`) is not an API key. See [oauth.md](oauth.md).

A `401` from a player route includes `WWW-Authenticate` with `resource_metadata` when `PUBLIC_BASE_URL` is set, so a client can discover the authorization server (RFC 9728).

Saga and group writes reject `ADMIN_TOKEN`, `created_by`, and user-identity headers. The signed-in account is the author.

## Groups

Session or `gst_`:

| Method | Path | |
| --- | --- | --- |
| `POST` | `/v1/groups` | Create. Caller becomes owner. |
| `GET` | `/v1/groups` | Groups the account belongs to. |
| `GET` | `/v1/groups/{id}/members` | Members of a group you belong to. |
| `GET` | `/v1/groups/{id}/leaderboard?period=all\|month\|week\|today` | Ranked characters in a group you belong to. `period` defaults to `all`. |
| `POST` | `/v1/groups/{id}/invites` | Owner. Body `{name, realm, battletag?}`. |
| `GET` | `/v1/groups/{id}/invites?status=pending` | Owner or member. Pending outgoing invites: character `name`, `realm`, `invited_at`. Other `status` values are `400`. |
| `DELETE` | `/v1/groups/{id}/invites/{inviteId}` | Owner revokes a pending invite. A member or outsider gets `403`. An accepted or declined invite is `409`. |
| `GET` | `/v1/me/invites` | Pending invites addressed to the caller (character or BattleTag). This does not create include-invites. `GET /v1/sync` still does, for alts that are not participating. |
| `POST` | `/v1/invites/{id}/accept` | Join the account. Marks that character participating. |
| `POST` | `/v1/invites/{id}/decline` | Permanent for that invite. |
| `POST` | `/v1/groups/{id}/leave` | Leave. If the owner leaves, the oldest remaining member becomes owner. |
| `DELETE` | `/v1/groups/{id}/members/{playerID}` | Owner removes a member. |
| `PUT` | `/v1/groups/{id}/webhook` | Owner. `{"url":"..."}` (empty clears). JSON event POSTs — see [webhooks.md](webhooks.md). |
| `PUT` | `/v1/groups/{id}/locale` | Owner. `en` or `nb`. |
| `PATCH` | `/v1/characters/{id}` | `{"participating":true}`. |

## Sagas

`GET /v1/sync` returns sagas in groups the player belongs to, that player's progress, and completions limited to those groups. Party credit for a kill counts only characters whose accounts are members of the same group.

`POST /v1/sagas` — any member. Body field `group` is a slug when it is a string. If the player is in exactly one group it may be omitted. Non-members get `403`.

`PATCH /v1/sagas/{id}` — the author. `changes` is required when the text changes. A cover-only upload does not bump the version.

`POST /v1/complete` — the character's account must be a member of the saga's group. Optional `started_at` (RFC3339) is the addon clock when the first goal of that saga gained progress. The server stores `duration_sec` = `completed_at` - `started_at` when the start is not after the finish. Both times are addon-reported and are not checked against the game server. A second finish for the same character and saga stays `already_completed`. The table can hold more than one finish row so repeatable sagas can be added later; that behavior is not enabled.

`GET /v1/sagas/{id}` is readable by any member of the saga's group. `?sort=completed_at` orders `completed_by` newest first. `?sort=completion_time` orders by `duration_sec` ascending, with missing durations last. The default order is oldest first.

`GET /v1/sync` includes `duration_sec` on completions and `leaderboards` for every group the player belongs to, one entry per period (`all`, `month`, `week`, `today`). Periods are calendar windows in Europe/Oslo. The count is distinct sagas, per character, and only characters whose account is still in the group. There are no points.

## Ops

| Method | Path | |
| --- | --- | --- |
| `DELETE` | `/v1/admin/sagas/{id}` | Hard-delete one saga. |
| `DELETE` | `/v1/admin/groups/{id}` | Delete the group and its sagas. |

Seed runs from `sagalore-api seed` or `make dev`. It does not run on server boot.

## Errors

`error` is the code and the contract. `message` is English. Clients may translate known codes. Group locale (`en` / `nb`) is for companion and addon chrome.

See [sagas-api.md](sagas-api.md) for the saga document and [self-hosting.md](self-hosting.md) for deployment.
