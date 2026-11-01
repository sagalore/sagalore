-- Invite prompts and the chat lines the companion posts.
-- The addon has no HTTP. SavedVariables hold the 12-hour snooze across /reload.
-- Pending join sagas are preloaded in SagaloreData with pending_invite_id; Accept
-- reveals them immediately without /reload.
local M = {}

local SNOOZE = 12 * 60 * 60

local function same(a, b)
    if type(a) ~= "string" or type(b) ~= "string" then
        return false
    end
    return string.lower(a) == string.lower(b)
end

function M.line(action, id)
    if type(id) ~= "string" or id == "" then
        return nil
    end
    if action ~= "accept" and action ~= "decline" then
        return nil
    end
    return "[Sagalore] INVITE " .. action .. "|" .. id
end

function M.snooze(db, id, now)
    if type(db) ~= "table" or type(id) ~= "string" or id == "" then
        return
    end
    if type(db.invite_snooze) ~= "table" then
        db.invite_snooze = {}
    end
    local at = tonumber(now) or 0
    db.invite_snooze[id] = at + SNOOZE
end

function M.snoozed(db, id, now)
    if type(db) ~= "table" or type(db.invite_snooze) ~= "table" then
        return false
    end
    local untilAt = tonumber(db.invite_snooze[id])
    local at = tonumber(now) or 0
    return untilAt ~= nil and untilAt > at
end

-- markAnswered hides an invite locally after Accept/Decline so the next one can show
-- before the companion rewrites SagaloreData.
function M.markAnswered(db, id)
    if type(db) ~= "table" or type(id) ~= "string" or id == "" then
        return
    end
    if type(db.answered_invites) ~= "table" then
        db.answered_invites = {}
    end
    db.answered_invites[id] = true
end

function M.answered(db, id)
    if type(db) ~= "table" or type(db.answered_invites) ~= "table" then
        return false
    end
    return db.answered_invites[id] == true
end

-- markAccepted reveals preloaded pending_invite_id entries without /reload.
function M.markAccepted(db, id)
    if type(db) ~= "table" or type(id) ~= "string" or id == "" then
        return
    end
    if type(db.accepted_invites) ~= "table" then
        db.accepted_invites = {}
    end
    db.accepted_invites[id] = true
end

function M.accepted(db, id)
    if type(db) ~= "table" or type(db.accepted_invites) ~= "table" then
        return false
    end
    return db.accepted_invites[id] == true
end

-- entryVisible is false for preloaded join sagas until Accept (or membership sync).
function M.entryVisible(db, entry)
    if type(entry) ~= "table" then
        return false
    end
    local pending = entry.pending_invite_id
    if type(pending) ~= "string" or pending == "" then
        return true
    end
    return M.accepted(db, pending)
end

-- reconcileAnswered clears answered_invites that are still pending in sync data so
-- a failed companion accept can re-prompt after /reload.
function M.reconcileAnswered(db, data)
    if type(db) ~= "table" or type(db.answered_invites) ~= "table" then
        return
    end
    if type(data) ~= "table" or type(data.invites) ~= "table" then
        return
    end
    local pending = {}
    for i = 1, #data.invites do
        local inv = data.invites[i]
        if type(inv) == "table" and type(inv.id) == "string" then
            pending[inv.id] = true
        end
    end
    for id in pairs(db.answered_invites) do
        if pending[id] and not M.accepted(db, id) then
            db.answered_invites[id] = nil
        end
    end
end

-- onlyPersonalGroups is true when every synced group is personal (or there are none).
function M.onlyPersonalGroups(data)
    if type(data) ~= "table" or type(data.groups) ~= "table" then
        return true
    end
    local any = false
    for i = 1, #data.groups do
        local g = data.groups[i]
        if type(g) == "table" then
            any = true
            if g.personal ~= true then
                return false
            end
        end
    end
    return any or true
end

-- prompt returns nil or { id, key, group, character }.
-- key is invite.join or invite.include.
function M.prompt(db, character, data, now)
    if type(data) ~= "table" or type(data.invites) ~= "table" then
        return nil
    end
    local name = character and character.name or ""
    local realm = character and character.realm or ""
    if name == "" then
        return nil
    end
    for i = 1, #data.invites do
        local inv = data.invites[i]
        if type(inv) == "table" and type(inv.id) == "string" and inv.id ~= "" then
            if not M.answered(db, inv.id) and not M.snoozed(db, inv.id, now)
                and same(inv.name, name) and same(inv.realm or "", realm) then
                local key = "invite.join"
                if inv.kind == "include" then
                    key = "invite.include"
                end
                return {
                    id = inv.id,
                    key = key,
                    group = inv.group_name or "",
                    character = name,
                }
            end
        end
    end
    return nil
end

-- waitingFor is an invite on this account that is not for the logged-in character.
-- Used to hint "log into Magni-Everlook" instead of failing silently.
function M.waitingFor(db, character, data, now)
    if type(data) ~= "table" or type(data.invites) ~= "table" then
        return nil
    end
    local name = character and character.name or ""
    local realm = character and character.realm or ""
    if name == "" then
        return nil
    end
    for i = 1, #data.invites do
        local inv = data.invites[i]
        if type(inv) == "table" and type(inv.id) == "string" and inv.id ~= "" then
            if not M.answered(db, inv.id) and not M.snoozed(db, inv.id, now) then
                local invName = inv.name or ""
                local invRealm = inv.realm or ""
                if invName ~= "" and not (same(invName, name) and same(invRealm, realm)) then
                    return {
                        id = inv.id,
                        name = invName,
                        realm = invRealm,
                        group = inv.group_name or "",
                    }
                end
            end
        end
    end
    return nil
end

SagaloreInvite = M
return M
