# Realtime

The companion used to learn about sagas by calling `GET /v1/sync` every ten minutes. It still does that as a fallback. The fast path is a WebSocket.

Addon chrome follows the group or companion language. This note is English.

## Socket

`GET /v1/ws`, `Authorization: Bearer` with the same Sagalore session (`gss_…`) or personal token (`gst_…`) as `/v1/sync`. A bad or missing token is `401` and the connection is not upgraded. The token is a header, not a query parameter, so it does not land in access logs.

The companion is not a browser. The server does not check `Origin`. The bearer is the credential.

Each text frame is one JSON object:

| `type` | Who receives it | When |
|---|---|---|
| `saga.created` | members of the saga's group | a saga row is inserted (`version` is 1) |
| `saga.updated` | members of the saga's group | `quests.version` changes and the saga stays or becomes active |
| `saga.retired` | members of the saga's group | `quests.version` changes and `active` goes from true to false |
| `saga.completed` | members of the saga's group | a new progress row with `status = completed` |
| `level.changed` | only that `player_id` | `characters.level` actually changes |
| `ping` | the socket that is being kept warm | every 25s, from the process that holds the socket |

```json
{"type":"saga.created","saga_id":"cf031479-7288-4508-b139-f23f1e322026","version":1}
{"type":"saga.updated","saga_id":"cf031479-7288-4508-b139-f23f1e322026","version":2}
{"type":"saga.retired","saga_id":"cf031479-7288-4508-b139-f23f1e322026","version":3}
{"type":"saga.completed","saga_id":"cf031479-7288-4508-b139-f23f1e322026","version":1}
{"type":"level.changed","player_id":"…"}
{"type":"ping"}
```

`version` on a completion is the quest version the player had loaded, not necessarily the current draft. `level.changed` has no `saga_id`.

The frame is a hint. The companion calls `GET /v1/sync` and rewrites `SagaloreData.lua`. It does not apply the event as the saga text. Empty fields are omitted. The payload stays far under Postgres `NOTIFY`'s 8000-byte cap.

A second completion of the same character and saga does not insert a row and does not emit `saga.completed`. Setting the same level again does not emit `level.changed`. Updating only `image_bytes` does not mention `version` or `active`, so a cover image waits for the next sync (the ten-minute poll, a later saga event, or a reconnect).

## Several API processes

Migration `007_realtime_notify.sql` installs triggers. On commit they `pg_notify('sagalore_events', …)`. Each API process holds **one dedicated connection** (`application_name = sagalore-realtime`) and `LISTEN`s. A pooled connection would be reused and drop the subscription.

You can run more than one API replica. Sticky sessions are not required: the replica that committed the saga and the replica that holds the player's socket can be different. Both hear the notify. Each replica fans the event out only to the sockets it accepted.

A new write path in `api/` does not need to call Go code. Bumping `quests.version` (what `UpdateQuest` already does) is enough. A path that writes some other table can emit the same JSON:

```sql
SELECT pg_notify(
  'sagalore_events',
  '{"type":"saga.updated","saga_id":"<uuid>","version":2}'
);
```

## Companion

`http://` API URLs become `ws://`, `https://` becomes `wss://`, path `/v1/ws`.

On each event except `ping`, and on every reconnect, the companion syncs and rewrites `SagaloreData.lua`. Bursts within 200ms become one sync. The ten-minute poll stays.

If the visible saga list changed, the companion logs a line (`Ny saga: …`, `Saga oppdatert: …`, and so on). It does **not** raise a desktop banner — group webhooks carry the player-facing alert when configured.

The first sync after the program starts stays quiet in that sense. Startup already asks for `/reload`.

WoW reads `SagaloreData.lua` only at login and `/reload`. The program cannot press keys in the client. The file also gets `notice` and `notice_id`. After `/reload` the addon prints that notice once in chat and remembers it in `SagaloreDB.seen_notice`.

Reconnect waits 1s, 2s, 4s, … capped at 60s, with ±20% jitter. A `401` clears the session and starts Battle.net login again (or the dev-token bypass). If no data frame arrives for 70 seconds the companion treats the socket as dead. The server's text `ping` is that data frame.

## Reverse proxies and multi-replica hosts

Host-agnostic expectations for putting TLS and load balancing in front of the API:

- The proxy must allow the HTTP/1.1 WebSocket upgrade on `GET /v1/ws`. Health checks can use `GET /healthz`.
- Prefer terminating TLS at the proxy. Companions use `wss://` when `PUBLIC_BASE_URL` is `https://`. Forward `X-Forwarded-Proto` when the app needs to know the public scheme.
- Many edges idle-close quiet HTTP/1.1 connections around 60 seconds. Keep WebSocket upgrades exempt from that idle limit when the platform allows it. The API still sends a protocol ping and a text `{"type":"ping"}` every 25 seconds for NATs and half-open links.
- Do not set Go `http.Server` `ReadTimeout` / `WriteTimeout` on the long-lived socket path; those would cut WebSockets. `ReadHeaderTimeout` (10s) only covers upgrade headers.
- Point companions at the public HTTPS API origin, not a plain `http://` edge hostname, once TLS is in front.
- On deploy, send `SIGTERM`. The process closes sockets with close code `1001` (Going Away), stops the `LISTEN` connection, then shuts the HTTP server down within 10 seconds. Companions reconnect and sync, so a saga committed during the rollout is not lost.
- Postgres `NOTIFY` is what makes a second replica correct. One replica's memory is not enough.

## Tests

```bash
export DATABASE_URL=postgres://sagalore:sagalore@127.0.0.1:5432/sagalore_test?sslmode=disable
go test ./internal/realtime ./internal/syncdata ./internal/notify ./internal/app ./api
```

`TestRealtimeTwoInstances` runs two listeners against one database, which is the two-replica case.
