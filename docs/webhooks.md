# Group webhooks

Each group may have one HTTPS (or HTTP) URL. The API POSTs a JSON event when something happens in that group. Formatting for Discord, Slack, or anything else belongs outside this repo (for example a Smia tool or a small relay).

Set or clear the URL as the group owner:

```http
PUT /v1/groups/{id}/webhook
Authorization: Bearer <token>
Content-Type: application/json

{"url":"https://example.com/hooks/sagalore"}
```

Optional opaque `meta` (JSON object) is stored on the group and copied onto every event as `meta`. The API does not interpret keys — receivers (for example a Smia Discord relay) decide what they mean. Omit `meta` to leave it unchanged; send `{}` to clear it.

```http
PUT /v1/groups/{id}/webhook
{"url":"https://smia.example/hooks/t/…","meta":{"discord_webhook_url":"https://discord.com/api/webhooks/…","alerts":["saga_created","saga_completed"]}}
```

```http
GET /v1/groups/{id}/webhook
```

Returns `{ "url", "configured", "meta" }` for the group owner.

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
| `join.joined` | Someone redeems an **open** join link |
| `join.requested` | Someone submits a **gated** join request |
| `join.approved` | An owner approves a gated join request |
| `join.rejected` | An owner rejects a gated join request |

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
    "active": true,
    "objectives": [
      { "id": "go-inn", "type": "go", "text": "Walk to the inn", "zone": "Elwynn Forest" },
      { "id": "talk", "type": "talk", "text": "Talk to the innkeeper", "npc": "Innkeeper Farley" }
    ],
    "image_url": "https://api.example.com/v1/public/sagas/…/image?exp=…&sig=…"
  },
  "character": {
    "name": "Magni",
    "realm": "Everlook",
    "race": "Dwarf",
    "class": "Paladin",
    "level": 12
  },
  "actor": { "name": "Magnus", "battletag": "Magnus#2231" },
  "changes": ["Nytt navn"],
  "meta": { "discord_webhook_url": "https://discord.com/api/webhooks/…", "alerts": ["saga_completed"] }
}
```

Fields other than `type`, `occurred_at`, and `group` are omitted when unused. `meta` is omitted when empty. `invite.accepted` includes `invite` (`{ "id": "…" }`) instead of `saga`. Join-link events include `join` (`id`, `link_id`, `mode`, `status`) and `characters` (selected roster); a single selected character is also mirrored on `character`. `changes` appears on updates when the author sent a change list.

On saga events, `saga.objectives` is the same structured list as the saga API (not a preformatted markdown string). `saga.image_url` is set when the saga has a cover and the API is configured for signed public image URLs; otherwise it is omitted. Integrators can render objectives however they like.

`character.realm` is the Classic realm name, or the Forever surname (display as `name` + `realm` for a full Forever character name).

The request uses `Content-Type: application/json` and `User-Agent: Sagalore-Webhook/1`. A non-2xx response is logged; there is no automatic retry beyond the delivery attempt.
