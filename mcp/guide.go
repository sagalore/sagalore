package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func init() {
	for _, t := range guideTools() {
		Register(t)
	}
}

func guideTools() []Tool {
	id := map[string]any{"type": "string", "description": "UUID from an earlier list or create call."}
	return []Tool{
		{
			Name: "how_it_works", Description: howItWorksDescription, Schema: map[string]any{
				"type": "object", "properties": map[string]any{},
			},
			ReadOnly: true,
			Call: func(context.Context, json.RawMessage) (string, error) {
				return howItWorksText, nil
			},
		},
		{
			Name: "get_download_instructions", Description: downloadDescription, ReadOnly: true,
			Schema: objSchema(map[string]any{
				"os":   map[string]any{"type": "string", "enum": []string{"macos", "windows"}, "description": "macos or windows."},
				"arch": map[string]any{"type": "string", "enum": []string{"arm64", "amd64"}, "description": "arm64 (Apple silicon) or amd64 (Intel Mac or Windows)."},
			}, "os", "arch"),
			Call: callDownload,
		},
		{
			Name: "list_groups", Description: listGroupsDescription, ReadOnly: true,
			Schema: objSchema(map[string]any{}),
			Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return currentAPI().call(ctx, "GET", "/v1/groups", nil)
			},
		},
		{
			Name: "create_group", Description: createGroupDescription,
			Schema: objSchema(map[string]any{
				"name":   map[string]any{"type": "string", "description": "Display name, for example Friends."},
				"slug":   map[string]any{"type": "string", "description": "Optional lowercase slug. Letters, digits, hyphens."},
				"locale": map[string]any{"type": "string", "enum": []string{"en", "nb"}, "description": "Optional chrome language. en or nb."},
			}, "name"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				body, err := object(args)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(asString(body["name"])) == "" {
					return "", fmt.Errorf("name is required")
				}
				return currentAPI().call(ctx, "POST", "/v1/groups", body)
			},
		},
		{
			Name: "list_group_members", Description: listMembersDescription, ReadOnly: true,
			Schema: objSchema(map[string]any{"group_id": id}, "group_id"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				gid, err := requiredID(args, "group_id")
				if err != nil {
					return "", err
				}
				return currentAPI().call(ctx, "GET", "/v1/groups/"+url.PathEscape(gid)+"/members", nil)
			},
		},
		{
			Name: "invite_to_group", Description: inviteDescription,
			Schema: objSchema(map[string]any{
				"group_id":  id,
				"name":      map[string]any{"type": "string", "description": "Character name."},
				"realm":     map[string]any{"type": "string", "description": "Realm, for example Everlook."},
				"battletag": map[string]any{"type": "string", "description": "Optional BattleTag only if the user typed it."},
			}, "group_id", "name", "realm"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				body, err := object(args)
				if err != nil {
					return "", err
				}
				gid := strings.TrimSpace(asString(body["group_id"]))
				if gid == "" || strings.TrimSpace(asString(body["name"])) == "" || strings.TrimSpace(asString(body["realm"])) == "" {
					return "", fmt.Errorf("group_id, name, and realm are required")
				}
				delete(body, "group_id")
				return currentAPI().call(ctx, "POST", "/v1/groups/"+url.PathEscape(gid)+"/invites", body)
			},
		},
		{
			Name: "list_pending_invites", Description: pendingInvitesDescription, ReadOnly: true,
			Schema: objSchema(map[string]any{"group_id": id}, "group_id"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				gid, err := requiredID(args, "group_id")
				if err != nil {
					return "", err
				}
				return currentAPI().call(ctx, "GET", "/v1/groups/"+url.PathEscape(gid)+"/invites?status=pending", nil)
			},
		},
		{
			Name: "revoke_invite", Description: revokeInviteDescription,
			Schema: objSchema(map[string]any{"group_id": id, "invite_id": id}, "group_id", "invite_id"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				gid, iid, err := twoIDs(args, "group_id", "invite_id")
				if err != nil {
					return "", err
				}
				return currentAPI().call(ctx, "DELETE", "/v1/groups/"+url.PathEscape(gid)+"/invites/"+url.PathEscape(iid), nil)
			},
		},
		{
			Name: "remove_member", Description: removeMemberDescription,
			Schema: objSchema(map[string]any{"group_id": id, "player_id": id}, "group_id", "player_id"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				gid, pid, err := twoIDs(args, "group_id", "player_id")
				if err != nil {
					return "", err
				}
				return currentAPI().call(ctx, "DELETE", "/v1/groups/"+url.PathEscape(gid)+"/members/"+url.PathEscape(pid), nil)
			},
		},
		{
			Name: "leave_group", Description: leaveGroupDescription,
			Schema: objSchema(map[string]any{"group_id": id}, "group_id"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				gid, err := requiredID(args, "group_id")
				if err != nil {
					return "", err
				}
				return currentAPI().call(ctx, "POST", "/v1/groups/"+url.PathEscape(gid)+"/leave", map[string]any{})
			},
		},
		{
			Name: "my_invites", Description: myInvitesDescription, ReadOnly: true,
			Schema: objSchema(map[string]any{}),
			Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return currentAPI().call(ctx, "GET", "/v1/me/invites", nil)
			},
		},
		{
			Name: "accept_invite", Description: acceptInviteDescription,
			Schema: objSchema(map[string]any{"invite_id": id}, "invite_id"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				id, err := requiredID(args, "invite_id")
				if err != nil {
					return "", err
				}
				return currentAPI().call(ctx, "POST", "/v1/invites/"+url.PathEscape(id)+"/accept", map[string]any{})
			},
		},
		{
			Name: "decline_invite", Description: declineInviteDescription,
			Schema: objSchema(map[string]any{"invite_id": id}, "invite_id"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				id, err := requiredID(args, "invite_id")
				if err != nil {
					return "", err
				}
				return currentAPI().call(ctx, "POST", "/v1/invites/"+url.PathEscape(id)+"/decline", map[string]any{})
			},
		},
		{
			Name: "list_characters", Description: listCharactersDescription, ReadOnly: true,
			Schema: objSchema(map[string]any{}),
			Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return currentAPI().call(ctx, "GET", "/v1/me/characters", nil)
			},
		},
		{
			Name: "list_sagas", Description: listSagasDescription, ReadOnly: true,
			Schema: objSchema(map[string]any{
				"character_id": map[string]any{"type": "string", "description": "Optional character UUID. Adds that character's status."},
			}),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				body, err := object(args)
				if err != nil {
					return "", err
				}
				path := "/v1/sagas"
				if id := strings.TrimSpace(asString(body["character_id"])); id != "" {
					path += "?character_id=" + url.QueryEscape(id)
				}
				return currentAPI().call(ctx, "GET", path, nil)
			},
		},
		{
			Name: "get_saga", Description: getSagaDescription, ReadOnly: true,
			Schema: objSchema(map[string]any{"saga_id": id}, "saga_id"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				id, err := requiredID(args, "saga_id")
				if err != nil {
					return "", err
				}
				return currentAPI().call(ctx, "GET", "/v1/sagas/"+url.PathEscape(id), nil)
			},
		},
		{
			Name: "create_saga", Description: createSagaDescription,
			Schema: createSagaSchema(),
			Call:   callCreateSaga,
		},
		{
			Name: "update_saga", Description: updateSagaDescription,
			Schema: objSchema(map[string]any{
				"saga_id":     id,
				"title":       map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"objectives":  map[string]any{"type": "array", "description": "Full replacement step list. Same shape as create_saga. Item steps require item_id and default to memory hold. show_count false hides the in-game count suffix."},
				"changes":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Short notes stored on the new version. Optional: when omitted, the API writes one line per changed field."},
				"image_url":   map[string]any{"type": "string", "description": "https URL of a png, jpeg, gif, or webp cover (max 8 MiB). The API fetches it. A cover-only call does not bump the version."},
				"party":       map[string]any{"type": "boolean"},
				"active":      map[string]any{"type": "boolean"},
				"min_level":   map[string]any{"type": "integer"},
			}, "saga_id"),
			Call: callUpdateSaga,
		},
		{
			Name: "archive_saga", Description: archiveSagaDescription,
			Schema: objSchema(map[string]any{"saga_id": id}, "saga_id"),
			Call: func(ctx context.Context, args json.RawMessage) (string, error) {
				id, err := requiredID(args, "saga_id")
				if err != nil {
					return "", err
				}
				return currentAPI().call(ctx, "PATCH", "/v1/sagas/"+url.PathEscape(id), map[string]any{"active": false})
			},
		},
	}
}

func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func createSagaSchema() map[string]any {
	return objSchema(map[string]any{
		"title":       map[string]any{"type": "string"},
		"description": map[string]any{"type": "string"},
		"party":       map[string]any{"type": "boolean", "description": "True when any group member may see it. False for a personal saga."},
		"group":       map[string]any{"description": "Tenant slug string when the player is in more than one group. Omit when they are in exactly one."},
		"objectives": map[string]any{
			"type":        "array",
			"description": "1 to 20 steps. Types: go, talk, item, kill, emote, sequence, level, instance_run.",
			"items":       map[string]any{"type": "object"},
		},
		"min_level":              map[string]any{"type": "integer"},
		"active":                 map[string]any{"type": "boolean", "description": "False keeps it a draft. Omitted means active."},
		"assigned_character_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"image_url":              map[string]any{"type": "string", "description": "https URL of a png, jpeg, gif, or webp cover (max 8 MiB). The API fetches it. Do not send bytes."},
	}, "title", "description", "party", "objectives")
}

func callCreateSaga(ctx context.Context, args json.RawMessage) (string, error) {
	body, err := object(args)
	if err != nil {
		return "", err
	}
	delete(body, "created_by")
	if strings.TrimSpace(asString(body["title"])) == "" || strings.TrimSpace(asString(body["description"])) == "" {
		return "", fmt.Errorf("title and description are required")
	}
	if _, ok := body["party"]; !ok {
		return "", fmt.Errorf("party is required (true or false)")
	}
	steps, ok := body["objectives"].([]any)
	if !ok || len(steps) == 0 {
		return "", fmt.Errorf("objectives must be a non-empty list of steps")
	}
	if len(steps) > 20 {
		return "", fmt.Errorf("at most 20 objectives")
	}
	return currentAPI().call(ctx, "POST", "/v1/sagas", body)
}

func callUpdateSaga(ctx context.Context, args json.RawMessage) (string, error) {
	body, err := object(args)
	if err != nil {
		return "", err
	}
	delete(body, "created_by")
	id := strings.TrimSpace(asString(body["saga_id"]))
	if id == "" {
		return "", fmt.Errorf("saga_id is required")
	}
	delete(body, "saga_id")
	if len(body) == 0 {
		return "", fmt.Errorf("send at least one field to change")
	}
	return currentAPI().call(ctx, "PATCH", "/v1/sagas/"+url.PathEscape(id), body)
}

func callDownload(_ context.Context, args json.RawMessage) (string, error) {
	body, err := object(args)
	if err != nil {
		return "", err
	}
	osName := strings.ToLower(strings.TrimSpace(asString(body["os"])))
	arch := strings.ToLower(strings.TrimSpace(asString(body["arch"])))
	var asset, note string
	switch {
	case osName == "macos" && arch == "arm64":
		asset = "sagalore-darwin-arm64.zip"
		note = "Apple silicon. Unzip and open Sagalore.app. Do not use Terminal or chmod. Gatekeeper will warn because the binary is unsigned."
	case osName == "macos" && arch == "amd64":
		asset = "sagalore-darwin-amd64.zip"
		note = "Intel Mac. Unzip and open Sagalore.app. Do not use Terminal or chmod. Gatekeeper will warn because the binary is unsigned."
	case osName == "windows" && arch == "amd64":
		asset = "sagalore-windows-amd64.exe"
		note = "Windows. SmartScreen will warn because the binary is unsigned. Run the exe."
	default:
		return "", fmt.Errorf("os must be macos or windows and arch must be arm64 or amd64; Windows is amd64 only")
	}
	return fmt.Sprintf("Download %s from https://github.com/sagalore/sagalore/releases/latest/download/%s\n%s\nThen sign in with Battle.net in the companion, enable the Sagalore addon, and type /reload in game. After that, use list_sagas and create_saga. New sagas appear after another /reload.", asset, asset, note), nil
}

func object(args json.RawMessage) (map[string]any, error) {
	if len(bytesTrim(args)) == 0 || string(args) == "null" {
		return map[string]any{}, nil
	}
	var body map[string]any
	if err := json.Unmarshal(args, &body); err != nil {
		return nil, fmt.Errorf("arguments must be an object")
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, nil
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func requiredID(args json.RawMessage, key string) (string, error) {
	body, err := object(args)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(asString(body[key]))
	if id == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return id, nil
}

func twoIDs(args json.RawMessage, a, b string) (string, string, error) {
	body, err := object(args)
	if err != nil {
		return "", "", err
	}
	left := strings.TrimSpace(asString(body[a]))
	right := strings.TrimSpace(asString(body[b]))
	if left == "" || right == "" {
		return "", "", fmt.Errorf("%s and %s are required", a, b)
	}
	return left, right, nil
}

const howItWorksDescription = `Short guide to Sagalore and the order to create a saga the player can actually see in game.

Call this first when someone is new, stuck, or about to author a saga. It has no arguments.

Example: how_it_works with {}.
The reply covers installing the companion and addon, Battle.net login, create_saga, objective types, the companion sync, and /reload.`

const howItWorksText = `Sagalore is a saga log for a World of Warcraft group (Classic and Forever). Three pieces:

1. The addon, inside the game. It has no HTTP. It tracks steps and opens with /sagalore or /sl. It reads SagaloreData.lua only at login or when the player types /reload.
2. The companion, a desktop app. It installs the addon, signs the player in with Battle.net, writes SagaloreData.lua, and posts completions the addon recorded.
3. The API. It stores groups and sagas. Character name, realm, race, class, and level come only from that Battle.net sync. Do not invent or hand-edit them.

Recommended flow:
1. Call get_download_instructions with os and arch (Apple silicon is macos + arm64, Intel Mac is macos + amd64, Windows is windows + amd64). The player installs the companion, signs in with Battle.net, enables the Sagalore addon, and types /reload.
2. Call list_groups. If they are in none, create_group. If they are in more than one, later saga calls must pass group as the slug.
3. Call create_saga. Example: title "An evening in Goldshire", description "Visit Lion's Pride Inn.", party false, objectives one step {"id":"inn","type":"go","text":"Stand in Goldshire","zone":"Elwynn Forest"}.
4. The companion syncs. Tell the player to type /reload. The saga shows in /sagalore. Until that reload, a new saga looks like it did nothing.
5. They play the steps. The addon tracks them. The companion posts the completion. You do not mark a saga complete from chat.

Objective types (use these names): go (be somewhere; memory visit latches, hold clears when they leave), talk (dialogue or /talk on an NPC; targeting is not enough), item (have N in bags; item_id is required; memory hold by default; show_count false hides the (have/need) suffix), kill (defeat N by name; optional group.level is exact for every member), emote (a token such as WAVE), sequence (ordered beats inside one within_sec window, 2 to 6 beats), level (reach a level), instance_run (leave N dungeons or raids).
At most 20 steps. Each step id is 1–40 characters, letters, digits, underscore, or hyphen. Do not send created_by. Do not use all_of or any_of for a new saga. Coordinates and zone names belong to the WoW version the saga was built for.

If /sagalore does nothing after a companion update, the usual fix is /reload. If the addon says update required, update the companion, then /reload.`

const downloadDescription = `FIRST tool when someone needs the Sagalore companion app and addon.

Required: os (macos or windows) and arch (arm64 or amd64). Apple silicon is macos + arm64. Intel Mac is macos + amd64. Windows is windows + amd64.

Example: {"os":"macos","arch":"arm64"}.
Returns the GitHub release download plus install facts (unsigned file, Gatekeeper or SmartScreen, Battle.net login, enable the addon, /reload). Say those steps in the language of the conversation. Keep filenames, URLs, /reload, /sagalore, and /sl unchanged. On Mac the asset is a zip with Sagalore.app. Do not tell them to chmod or use Terminal.`

const listGroupsDescription = `List Sagalore groups the signed-in Battle.net account belongs to.

Example: {} returns id, slug, name, locale, and role (owner or member).
Call this before create_saga. If the list has more than one group, pass that slug as group on the saga. If it is empty, create_group first.`

const createGroupDescription = `Create a Sagalore group. The signed-in player becomes the owner.

Example: {"name":"Friends","slug":"friends","locale":"en"}.
slug is optional lowercase hyphens. locale is en or nb. Then create_saga in that group.`

const listMembersDescription = `List members of a group the player belongs to.

Example: {"group_id":"<id from list_groups>"} returns player_id, battletag, display_name, and role (owner or member).
Use player_id with remove_member. Do not copy a battletag into other tools unless the user typed it.`

const inviteDescription = `Owner-only. Invite a character into a group by name and realm.

Example: {"group_id":"<id>","name":"Mira","realm":"Everlook"}.
The friend accepts in the addon after they sign in with Battle.net and type /reload. Pass battletag only when the user wrote it.`

const pendingInvitesDescription = `Owner or member. List invites that have not been accepted yet.

Example: {"group_id":"<id from list_groups>"}.
Returns the invited character name and realm plus invite_id for revoke_invite.`

const revokeInviteDescription = `Owner-only. Cancel a pending invite so that character can no longer accept it.

Example: {"group_id":"<id>","invite_id":"<id from list_pending_invites>"}.
Confirm with the user first.`

const removeMemberDescription = `Owner-only. Remove a player from a group you own.

Example: {"group_id":"<id>","player_id":"<id from list_group_members>"}.
Their characters stop seeing that group's sagas. Confirm with the user first.`

const leaveGroupDescription = `Leave a group the signed-in account belongs to.

Example: {"group_id":"<id from list_groups>"}.
Their characters stop seeing that group's sagas. Confirm with the user first. If the owner leaves, the oldest remaining member becomes owner.`

const myInvitesDescription = `List pending group invites addressed to the signed-in account's characters.

Example: {} returns invite id, group, character, and realm.
Pass invite_id to accept_invite or decline_invite.`

const acceptInviteDescription = `Accept a pending invite for one of the player's characters.

Example: {"invite_id":"<id from my_invites>"}.
That Battle.net account joins the group and the character is marked participating. Tell them to type /reload to see the sagas.`

const declineInviteDescription = `Decline a pending invite.

Example: {"invite_id":"<id from my_invites>"}.
Confirm with the user first. Decline sticks.`

const listCharactersDescription = `List characters already synced for the signed-in Battle.net account.

Example: {}.
Name, realm, race, class, and level come from the companion sync only. There is no create, edit, or delete character tool. If the list is empty, the player must run the companion on a logged-in character and type /reload. backstory is the only note an owner can set, and it is not a character stat.`

const listSagasDescription = `List sagas visible to the signed-in player.

Example: {} or {"character_id":"<id from list_characters>"} to include that character's status.
Zone names and coordinates are for the WoW version the saga was built for. Do not reuse them on another version without checking.`

const getSagaDescription = `Fetch one saga by saga_id. The reply is the saga JSON: steps, description, versions, and completion activity. There is no HTML view.

Example: {"saga_id":"<id from list_sagas or create_saga>"}.
Use this before update_saga so you send a complete objectives list.`

const createSagaDescription = `Create a saga as the signed-in player. Do not send created_by.

Required: title, description, party (true or false), objectives (1 to 20 steps).
Optional: group (tenant slug; omit when the player is in exactly one group), active (false is a draft), min_level, assigned_character_ids, image_url (https cover the API fetches: png, jpeg, gif, or webp, max 8 MiB).

Example: {"title":"An evening in Goldshire","description":"Visit Lion's Pride Inn.","party":false,"objectives":[{"id":"inn","type":"go","text":"Stand in Goldshire","zone":"Elwynn Forest"}]}.

Step fields: id, type, text, plus type-specific fields.
- go: be at a place. zone and/or map_id, x, y. memory "visit" (default) latches; "hold" is only while they are there.
- talk: npc name. Completes on that NPC's dialogue, a right-click, or /talk. Targeting alone is not enough.
- item: item_id is required. item_name is optional display text. count defaults to 1 and is still tracked when omitted. memory "hold" (default) clears when the bags do. show_count false hides the in-game (have/need) suffix and does not append the item name, so the step text stands alone. Omit show_count to keep the suffix.
- kill: npc name, count. Optional group object {level, size, instance}. level is the exact level of every member.
- emote: emote token such as "WAVE" or "/wave", optional npc and zone.
- sequence: within_sec plus 2 to 6 beats (each beat is a small go, talk, emote, or kill).
- level: level to reach.
- instance_run: count of dungeon or raid leaves. kinds is ["party"] and/or ["raid"].

Constraints: at most 20 steps. id is 1–40 characters (letters, digits, underscore, hyphen). Do not use all_of, any_of, or objectives_ordered for a new saga; the saga itself is the AND, and a timed order is one sequence step. After this call succeeds, the companion syncs and the player must type /reload before the saga shows in game.`

const updateSagaDescription = `Patch a saga the signed-in player authored. Only sagas they can edit are accepted.

Example: {"saga_id":"<id>","title":"An evening in Goldshire","changes":["Clearer title"]}.
When steps change, send the full objectives array (same types and constraints as create_saga), not a single edited step. Item steps require item_id and default to memory "hold". show_count false hides the in-game count suffix. Text or step changes bump the saga version. changes is an optional list of short notes; when it is omitted the API stores one line per changed field (for example "Updated title"). Send changes when those words should be yours. image_url is an https cover the API fetches (png, jpeg, gif, or webp, max 8 MiB). A cover-only patch does not bump the version. Players who already loaded the old version keep that version until they /reload. Do not send created_by.`

const archiveSagaDescription = `Retire a saga the player authored so companions stop offering it (active=false).

Example: {"saga_id":"<id from list_sagas>"}.
Does not delete completions or history. Confirm with the user first. They should type /reload so the addon drops it.`
