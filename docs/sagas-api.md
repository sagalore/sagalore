# Saga documents

`POST /v1/sagas` and `PATCH /v1/sagas/{id}` take JSON. The signed-in player is the author. Do not send `created_by`.

```json
{
  "title": "An evening in Goldshire",
  "description": "Visit Lion's Pride Inn.",
  "party": true,
  "group": "tutorial",
  "objectives": [
    {"id": "inn", "type": "go", "text": "Stand in Goldshire", "zone": "Elwynn Forest"}
  ]
}
```

`group` as a string is the tenant slug. Omit it when the player is in exactly one group.

The party-size rule is a different field with the same name when the value is an object. Do not rename it. On a kill step it is `group` inside the objective (`level`, `size`, `instance`). On a saga it is the roster checked at completion (`size`, `min_level`, `instance`). To send both a slug and a party rule, put `slug` inside the object:

```json
"group": {"slug": "tutorial", "size": 4, "min_level": 17, "instance": "The Deadmines"}
```

Saga titles and objective text are author content. They are not translated. Chrome around them uses the English or Norwegian catalog.

A `talk` step (older name `interact`) completes when the player opens that NPC's dialogue or uses the object. Targeting the NPC is not enough. If the NPC has no gossip window, the step also completes when the player right-clicks them, or targets them and uses `/talk`. The tracker does not append the player's current zone or subzone to a goal line.

A new saga starts at version 1. Text edits bump the version. `changes` is the note stored on that version. When a patch omits it, the server writes one short line per changed field. Send `changes` when the note should be the author's own words. Completions keep the version the character had loaded.

`GET /v1/sagas/{id}` returns that JSON document (steps, description, versions, completions). It does not return an HTML page.

An item step requires `item_id`. `item_name` is optional display text. Omitted `count` is stored as 1. `memory` defaults to `hold` (it clears when the bags do). `show_count: false` hides the in-game `(have/need)` suffix and does not append the item name after the step text, so the author's sentence stands alone. The count is still tracked. Omit `show_count` to keep the suffix. Older addons ignore the flag and keep showing the suffix until they are updated.

```json
{"id": "loaf", "type": "item", "text": "Have a fresh loaf", "item_id": 4540, "show_count": false}
```

`GET /v1/sync` is what the companion writes into `SagaloreData.lua`. The addon reads it on `/reload`. Sync includes `leaderboards` (one board per group and period) and `duration_sec` on each completion.

`POST /v1/complete` accepts optional `started_at`. The addon sets that when the first goal gains progress and the companion forwards it. `duration_sec` in the response is `completed_at` minus `started_at`, in seconds, or omitted when the start is missing or later than the finish.

`GET /v1/sagas/{id}?sort=completion_time` lists `completed_by` fastest first. `sort=completed_at` lists newest first. Any group member can read that saga. A missing `sort` stays oldest first.

Covers: `PUT /v1/sagas/{id}/image`, or `image_url` on create and patch. `image_url` is an https URL the API fetches itself (public host, port 443, png / jpeg / gif / webp, at most 8 MiB). A presigned upload URL is a different path: the chat client in use may need that host allowed before the PUT succeeds. The companion converts the stored image to TGA under `Interface/AddOns/Sagalore/media/`. Signed public URLs use `PUBLIC_BASE_URL` and `IMAGE_URL_SECRET`. The response field `image_url` is the stored cover path. A cover-only update does not bump the saga version.
