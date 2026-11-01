# Group webhooks

Each group may have one HTTPS (or HTTP) URL. The API POSTs a JSON event when something happens in that group. Formatting for Discord, Slack, or anything else belongs outside this repo (for example a Smia tool or a small relay).

Set or clear the URL as the group owner:

```http
PUT /v1/groups/{id}/webhook
Authorization: Bearer <token>
Content-Type: application/json

{"url":"https://example.com/hooks/sagalore"}
```

Empty `url` clears the webhook. New groups stay quiet until an owner sets one.

On bootstrap, `SAGALORE_BOOTSTRAP_WEBHOOK_URL` is copied once onto the bootstrap group when that group's webhook is still empty. After that the group row is the source of truth.

## Events

| `type` | When |
| --- | --- |
| `saga.created` | A saga is created (or announced) |
| `saga.updated` | A significant edit bumps the saga version |
| `saga.retired` | A saga is set inactive |
| `saga.completed` | A character finishes a saga for the first time |
| `invite.accepted` | Someone accepts a group invite |

Delivery is asynchronous. Completions set `webhook_posted` on the progress row after a successful POST.

## Payload

```json
{
  "type": "saga.completed",
  "occurred_at": "2026-10-31T12:00:00Z",
  "group": { "id": "…", "slug": "tutorial", "name": "Tutorial" },
  "saga": {
    "id": "…",
    "title": "An evening in Goldshire",
    "version": 1,
    "description": "Visit the inn.",
    "active": true
  },
  "character": {
    "name": "Magni",
    "realm": "Everlook",
    "race": "Dwarf",
    "class": "Paladin",
    "level": 12
  },
  "actor": { "name": "Magnus", "battletag": "Magnus#2231" },
  "changes": ["Nytt navn"]
}
```

Fields other than `type`, `occurred_at`, and `group` are omitted when unused. `invite.accepted` includes `invite` (`{ "id": "…" }`) instead of `saga`. `changes` appears on updates when the author sent a change list.

The request uses `Content-Type: application/json` and `User-Agent: Sagalore-Webhook/1`. A non-2xx response is logged; there is no automatic retry beyond the delivery attempt.
