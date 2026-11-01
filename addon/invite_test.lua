-- Invite dialog outcomes, snooze across a simulated reload, and English fallback.
local dir = arg[0]:match("^(.*)/") or "."

dofile(dir .. "/Sagalore/Locales/enUS.lua")
dofile(dir .. "/Sagalore/Locales/nbNO.lua")
dofile(dir .. "/Sagalore/Locales/locale.lua")
local Invite = dofile(dir .. "/Sagalore/SagaloreInvite.lua")

local function assertTrue(cond, msg)
    if not cond then
        error(msg or "assertion failed", 2)
    end
end

local id = "11111111-1111-1111-1111-111111111111"
assertTrue(Invite.line("accept", id) == "[Sagalore] INVITE accept|" .. id, "accept line")
assertTrue(Invite.line("decline", id) == "[Sagalore] INVITE decline|" .. id, "decline line")
assertTrue(Invite.line("later", id) == nil, "later is client-only")

local db = {}
local now = 1700000000
Invite.snooze(db, id, now)
assertTrue(Invite.snoozed(db, id, now + 10), "snoozed immediately")
-- /reload keeps SavedVariables; the same table is what the addon reads next.
local reloaded = db
assertTrue(Invite.snoozed(reloaded, id, now + 12 * 3600 - 1), "snooze survives reload")
assertTrue(not Invite.snoozed(reloaded, id, now + 12 * 3600 + 1), "snooze expires")

local id2 = "22222222-2222-2222-2222-222222222222"
local data = {
    invites = {
        { id = id, name = "Magni", realm = "Everlook", group_name = "Tutorial", kind = "join" },
        { id = id2, name = "Magni", realm = "Everlook", group_name = "Gutar", kind = "include" },
    },
}
local character = { name = "Magni", realm = "Everlook" }
local prompt = Invite.prompt({}, character, data, now)
assertTrue(prompt and prompt.key == "invite.join" and prompt.group == "Tutorial", "join prompt")
Invite.markAnswered(db, id)
prompt = Invite.prompt(db, character, data, now)
assertTrue(prompt and prompt.key == "invite.include" and prompt.group == "Gutar", "next invite after answer")
Invite.snooze(db, id2, now)
assertTrue(Invite.prompt(db, character, data, now + 5) == nil, "snooze hides the dialog")

assertTrue(Invite.entryVisible({}, { title = "Open" }), "no pending is visible")
assertTrue(not Invite.entryVisible({}, { pending_invite_id = id }), "pending hidden until accept")
Invite.markAccepted(db, id)
assertTrue(Invite.entryVisible(db, { pending_invite_id = id }), "accepted pending is visible")

local failID = "33333333-3333-3333-3333-333333333333"
local pendingData = { invites = { { id = failID, name = "Magni", realm = "Everlook", group_name = "Tutorial", kind = "join" } } }
db.answered_invites[failID] = true
Invite.reconcileAnswered(db, pendingData)
assertTrue(not Invite.answered(db, failID), "failed accept re-prompts after reconcile")
assertTrue(Invite.onlyPersonalGroups({ groups = { { name = "Personal", personal = true } } }), "only personal")
assertTrue(not Invite.onlyPersonalGroups({ groups = { { name = "Personal", personal = true }, { name = "Gutar" } } }), "friend group")

local other = { name = "Thrall", realm = "Everlook" }
local wait = Invite.waitingFor({}, other, data, now)
assertTrue(wait and wait.name == "Magni" and wait.realm == "Everlook" and wait.group == "Tutorial", "wrong character wait")
assertTrue(Invite.waitingFor({}, character, data, now) == nil, "matching character has no wait hint")
assertTrue(Invite.prompt({}, other, data, now) == nil, "wrong character has no prompt")

SagaloreLanguage = "nb"
assertTrue(Sagalore_L("invite.join") == "Bli med i %s?", "nb join")
assertTrue(Sagalore_L("invite.accept") == "Ja", "nb accept")
assertTrue(Sagalore_L("invite.decline") == "Nei", "nb decline")
assertTrue(Sagalore_L("invite.later") == "Ikke nå", "nb later")

SagaloreLocale.en["test.only_en"] = "English fallback"
SagaloreLanguage = "nb"
assertTrue(Sagalore_L("test.only_en") == "English fallback", "missing nb key uses English")

SagaloreLanguage = "en"
assertTrue(Sagalore_L("invite.join") == "Join %s?", "en join")

for key in pairs(SagaloreLocale.nb) do
    if SagaloreLocale.en[key] == nil then
        error("nb key missing from en: " .. key)
    end
end

print("invite_test ok")
