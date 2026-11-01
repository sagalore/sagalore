local src = debug.getinfo(1, "S").source
local dir = src:match("^@(.*)[/\\][^/\\]+$") or "."
local M = dofile(dir .. "/Sagalore/SagaloreObjectives.lua")

local function assertTrue(cond, msg)
    if not cond then
        error(msg or "assertion failed", 2)
    end
end

local objectives = {
    { id = "place", type = "location", text = "Goldshire", map_id = 37, x = 0.438, y = 0.658, radius = 0.05 },
    { id = "talk", type = "interact", text = "Farley", npc = "Innkeeper Farley" },
    { id = "water", type = "collect", text = "Vann", item_id = 159, count = 1 },
}

local steps, done = M.evaluate(objectives, { map_id = 37, x = 0.44, y = 0.66 }, {})
assertTrue(steps.place.done, "location should match")

steps, done = M.evaluate(objectives, { map_id = 1429, x = 0.44, y = 0.66 }, {})
assertTrue(steps.place.done, "classic era map id still matches the same coordinates")

steps, done = M.evaluate(objectives, { map_id = 1429, x = 44, y = 66 }, {})
assertTrue(steps.place.done, "percent coordinates match")
assertTrue(not steps.talk.done, "talk is still locked until we pass saved done")
assertTrue(not done, "not finished")

steps, done = M.evaluate(objectives, { npc = "Innkeeper Farley", items = { [159] = 5 } }, {})
assertTrue(not steps.talk.done and not steps.water.done, "later steps stay locked")

steps, done = M.evaluate(objectives, {
    map_id = 37, x = 0.90, y = 0.10, npc = "Innkeeper Farley", items = { [159] = 5 },
}, { place = { done = true } })
assertTrue(steps.place.done, "stays done away from the inn")
assertTrue(steps.talk.done and steps.water.done and done, "later steps complete in the same pass")

steps, done = M.evaluate(objectives, { items = { ["159"] = 1 } }, {
    place = { done = true }, talk = { done = true },
})
assertTrue(steps.water.done and done, "all done")

steps, done = M.evaluate({
    { id = "kobold", type = "kill", npc = "Kobold Vermin", count = 2 },
}, { kill_name = "Kobold Vermin", kill_secret = true }, {})
assertTrue(steps.kobold.count == 0 and not steps.kobold.done, "secret kill ignored")

steps, done = M.evaluate({
    { id = "kobold", type = "kill", npc = "Kobold Vermin", count = 2 },
}, { kill_name = "kobold vermin" }, { kobold = { count = 1 } })
assertTrue(steps.kobold.count == 2 and steps.kobold.done, "kill increments")

steps, done = M.evaluate({}, {}, {})
assertTrue(not done, "empty quest does not complete")

steps, done = M.evaluate({
    { id = "obj", type = "interact", object = "Mailbox" },
}, { object = "Mailbox" }, {})
assertTrue(steps.obj.done and done, "object interact")
steps = M.evaluate({
    { id = "obj", type = "interact", object = "Mailbox" },
}, { npc = "Mailbox" }, {})
assertTrue(steps.obj.done, "gossip name counts as object")

steps, done = M.evaluate(objectives, { object = "Innkeeper Farley" }, { place = { done = true } }, false)
assertTrue(not steps.talk.done, "targeting alone is not a talk")
steps, done = M.evaluate(objectives, { npc = "Innkeeper Farley" }, { place = { done = true } }, false)
assertTrue(steps.talk.done, "open dialogue counts as a talk")
steps, done = M.evaluate({
    { id = "farley", type = "talk", text = "Snakk", npc = "Innkeeper Farley" },
}, { talk_name = "Innkeeper Farley" }, {}, false)
assertTrue(steps.farley.done, "pending talk_name latches visit")

steps, done = M.evaluate({
    { id = "farley", type = "talk", text = "Snakk", npc = "Innkeeper Farley" },
}, { emote = "TALK", emote_target = "Innkeeper Farley" }, {}, false)
assertTrue(steps.farley.done and done, "/talk on the targeted NPC completes a talk")
steps, done = M.evaluate({
    { id = "farley", type = "interact", text = "Snakk", npc = "Innkeeper Farley" },
}, { emote = "/talk", emote_target = "Innkeeper Farley" }, {}, false)
assertTrue(steps.farley.done, "/talk slash token matches the interact alias")
steps, done = M.evaluate({
    { id = "farley", type = "talk", text = "Snakk", npc = "Farley" },
}, { emote = "TALK", emote_target = "Innkeeper Farley" }, {}, false)
assertTrue(steps.farley.done, "/talk matches a listed NPC name")
steps, done = M.evaluate({
    { id = "farley", type = "talk", text = "Snakk", npc = "Innkeeper Farley" },
}, { emote = "TALK", emote_target = "", emote_text = "You talk to Innkeeper Farley." }, {}, false)
assertTrue(steps.farley.done, "/talk chat line names the NPC")
steps, done = M.evaluate({
    { id = "farley", type = "talk", text = "Snakk", npc = "Innkeeper Farley" },
}, { emote = "TALK", emote_target = "Stormwind Guard" }, {}, false)
assertTrue(not steps.farley.done, "/talk on the wrong NPC does not count")
steps, done = M.evaluate({
    { id = "farley", type = "talk", text = "Snakk", npc = "Innkeeper Farley" },
}, { emote = "TALK" }, {}, false)
assertTrue(not steps.farley.done, "/talk with no target does not count")
steps, done = M.evaluate({
    { id = "farley", type = "talk", text = "Snakk", npc = "Innkeeper Farley" },
}, { emote = "SALUTE", emote_target = "Innkeeper Farley" }, {}, false)
assertTrue(not steps.farley.done, "a different emote is not a talk")
steps, done = M.evaluate({
    { id = "box", type = "talk", text = "Post", object = "Mailbox" },
}, { emote = "TALK", emote_target = "Mailbox" }, {}, false)
assertTrue(steps.box.done, "/talk on a named object completes the step")
steps, done = M.evaluate({
    { id = "greet", type = "sequence", text = "Snakk, så hils", within_sec = 30, beats = {
        { id = "hi", type = "talk", text = "Snakk", npc = "Innkeeper Farley" },
        { id = "salute", type = "emote", text = "Hils", emote = "SALUTE", npc = "Innkeeper Farley" },
    } },
}, {
    emote = "TALK", emote_target = "Innkeeper Farley",
    time = { ["local"] = { unix = 2000 } },
}, {}, false)
assertTrue(not steps.greet.done and steps.greet.beat == 2 and steps.greet.consumed_emote, "/talk advances a talk beat")

-- Any-order: later goals can complete without earlier ones.
steps, done = M.evaluate(objectives, { npc = "Innkeeper Farley", items = { [159] = 1 } }, {}, false)
assertTrue(not steps.place.done, "location still open")
assertTrue(steps.talk.done and steps.water.done, "interact and collect without place")
assertTrue(not done, "saga not finished until every goal is done")

steps, done = M.evaluate({
    { id = "place", type = "location", text = "Inn", subzone = "Goldshire|Lion's Pride Inn", map_id = 37, x = 0.438, y = 0.658, radius = 0.05 },
}, { minimap = "Lion's Pride Inn" }, {})
assertTrue(steps.place.done, "minimap label completes the inn when subzone text is empty")

steps, done = M.evaluate({
    { id = "place", type = "location", text = "Inn", subzone = "Goldshire|Lion's Pride Inn", map_id = 37, x = 0.438, y = 0.658, radius = 0.01 },
}, { subzone = "Goldshire", map_id = 999, x = 0.1, y = 0.1 }, {})
assertTrue(steps.place.done, "goldshire subzone completes just outside the inn")

steps, done = M.evaluate({
    { id = "place", type = "location", text = "Stå i eller rett utenfor Lion's Pride Inn i Goldshire", map_id = 37, x = 0.9, y = 0.9, radius = 0.01 },
}, { minimap = "Lion's Pride Inn" }, {})
assertTrue(steps.place.done, "objective wording matches the minimap when subzone is missing")

local night = { time = { ["local"] = { hour = 23, min = 15, unix = 1760000000 } } }
local day = { time = { ["local"] = { hour = 12, min = 0, unix = 1760000000 } } }
steps, done = M.evaluate({
    { id = "salute", type = "emote", text = "Hils", emote = "/salute", npc = "Stormwind Guard",
      map_id = 84, x = 0.50, y = 0.50, radius = 0.05,
      when = { clock = "local", start = "22:00", ["end"] = "02:00" } },
}, {
    emote = "SALUTE", emote_target = "Stormwind Guard",
    emote_map_id = 84, emote_x = 0.51, emote_y = 0.50,
    time = night.time,
}, {})
assertTrue(steps.salute.done and done, "emote at the guard during the window")

steps, done = M.evaluate({
    { id = "salute", type = "emote", text = "Hils", emote = "SALUTE", npc = "Stormwind Guard" },
}, { emote = "salute", emote_target = "Innkeeper Farley", time = night.time }, {})
assertTrue(not steps.salute.done, "wrong emote target")

steps, done = M.evaluate({
    { id = "salute", type = "emote", text = "Hils", emote = "SALUTE", npc = "Stormwind Guard" },
}, { emote = "SALUTE", emote_target = "Stormwind Guard" }, {})
assertTrue(steps.salute.done, "emote matches")
steps, done = M.evaluate({
    { id = "salute", type = "emote", text = "Hils", emote = "SALUTE", npc = "Stormwind Guard" },
}, {}, { salute = { done = true } })
assertTrue(steps.salute.done and done, "emote stays done after pending clears")

steps, done = M.evaluate({
    { id = "salute", type = "emote", text = "Hils", emote = "WAVE",
      when = { start = "22:00", ["end"] = "02:00" } },
}, { emote = "WAVE", time = day.time }, {})
assertTrue(not steps.salute.done, "daytime emote stays open")

steps, done = M.evaluate({
    { id = "both", type = "all_of", text = "Sted og vann", objectives = {
        { id = "inn", type = "location", text = "Goldshire", zone = "Elwynn Forest" },
        { id = "water", type = "collect", text = "Vann", item_id = 159, count = 1 },
    } },
}, { zone = "Elwynn Forest", items = { [159] = 1 } }, {}, false)
assertTrue(steps.both.done and steps.inn.done and steps.water.done and done, "all_of is true together")

steps, done = M.evaluate({
    { id = "both", type = "all_of", text = "Sted og vann", objectives = {
        { id = "inn", type = "location", text = "Goldshire", zone = "Elwynn Forest" },
        { id = "water", type = "collect", text = "Vann", item_id = 159, count = 1 },
    } },
}, { zone = "Stormwind City", items = { [159] = 1 } }, { inn = { done = true } }, false)
assertTrue(not steps.both.done and not steps.inn.done, "nested location is live, not sticky")

steps, done = M.evaluate({
    { id = "either", type = "any_of", text = "Ett av dem", objectives = {
        { id = "talk", type = "interact", text = "Snakk", npc = "Farley" },
        { id = "bow", type = "emote", text = "Bukk", emote = "BOW" },
    } },
}, { emote = "BOW" }, {}, false)
assertTrue(steps.either.done and steps.bow.done and not steps.talk.done and done, "any_of emote")

steps, done = M.evaluate({
    { id = "stay", type = "location", text = "Vakt", zone = "Elwynn Forest",
      when = { clock = "realm", starts_at = "2026-10-24T20:00:00Z", ends_at = "2026-10-25T04:00:00Z" } },
}, { zone = "Elwynn Forest", time = { realm = { hour = 21, min = 0, unix = 1792875600 } } }, {}, false)
assertTrue(steps.stay.done, "realm date range includes this instant")

steps, done = M.evaluate({
    { id = "stay", type = "location", text = "Vakt", zone = "Elwynn Forest" },
}, { zone = "Elwynn Forest", time = { game = { hour = 10, min = 0 } } }, {}, false, { clock = "game", start = "22:00", ["end"] = "05:00" })
assertTrue(not steps.stay.done and not done, "saga game-time window blocks completion")

steps, done = M.evaluate({
    { id = "stay", type = "location", text = "Vakt", zone = "Elwynn Forest" },
}, { zone = "Elwynn Forest", time = { game = { hour = 23, min = 0 } } }, {}, false, { clock = "game", start = "22:00", ["end"] = "05:00" })
assertTrue(steps.stay.done and done, "saga window open")

local group = {
    instance = "The Deadmines",
    zone = "The Deadmines",
    members = {
        { name = "Magni", level = 18 },
        { name = "Mira", level = 17 },
        { name = "Pip", level = 12 },
        { name = "Ada", level = 20 },
    },
}
assertTrue(not M.groupOK({ size = 4, min_level = 17, instance = "Deadmines" }, group), "Pip is below 17 so only 3 count")
assertTrue(M.groupOK({ size = 3, min_level = 17, instance = "The Deadmines|Deadmines" }, group), "three at 17 in the instance")
assertTrue(not M.groupOK({ size = 3, min_level = 17, instance = "Stormwind Stockade" }, group), "wrong instance")
assertTrue(M.groupOK(nil, {}), "no group rule")

-- item type + CLAP alias for APPLAUD.
steps, done = M.evaluate({
    { id = "water", type = "item", text = "Vann", item_id = 159, count = 1 },
}, { items = { [159] = 2 } }, {}, false)
assertTrue(steps.water.done and done, "item 2/1 is done")
steps, done = M.evaluate({
    { id = "clap", type = "emote", text = "Klapp", emote = "APPLAUD", npc = "Brother Wilhelm" },
}, { emote = "CLAP", emote_target = "Brother Wilhelm" }, {}, false)
assertTrue(steps.clap.done and done, "CLAP matches APPLAUD")

-- memory: visit latches, hold follows the world.
steps, done = M.evaluate({
    { id = "inn", type = "go", text = "Stå her", memory = "hold", zone = "Elwynn Forest" },
}, { zone = "Elwynn Forest" }, {}, false)
assertTrue(steps.inn.done, "hold is true while here")
steps, done = M.evaluate({
    { id = "inn", type = "go", text = "Stå her", memory = "hold", zone = "Elwynn Forest" },
}, { zone = "Stormwind City" }, { inn = { done = true } }, false)
assertTrue(not steps.inn.done, "hold clears after leaving")
steps, done = M.evaluate({
    { id = "inn", type = "go", text = "Besøk", zone = "Elwynn Forest" },
}, { zone = "Stormwind City" }, { inn = { done = true } }, false)
assertTrue(steps.inn.done, "visit stays ticked")
steps, done = M.evaluate({
    { id = "water", type = "item", text = "Vann", item_id = 159 },
}, { items = {} }, { water = { done = true } }, false)
assertTrue(not steps.water.done, "item defaults to hold")

-- sequence: clap, then rude within 30s. Wrong emote does nothing.
local trick = {
    { id = "trick", type = "sequence", text = "Klapp, så frekk", within_sec = 30, beats = {
        { id = "clap", type = "emote", text = "Klapp", emote = "APPLAUD", npc = "Brother Wilhelm" },
        { id = "rude", type = "emote", text = "Frekk", emote = "RUDE", npc = "Innkeeper Farley" },
    } },
}
steps, done = M.evaluate(trick, {
    emote = "WAVE", emote_target = "Brother Wilhelm",
    time = { ["local"] = { unix = 1000 } },
}, {}, false)
assertTrue(not steps.trick.done and (tonumber(steps.trick.beat) or 1) == 1, "wrong emote does not start")
steps, done = M.evaluate(trick, {
    emote = "APPLAUD", emote_target = "Brother Wilhelm",
    time = { ["local"] = { unix = 1000 } },
}, {}, false)
assertTrue(not steps.trick.done and steps.trick.beat == 2 and steps.trick.started_at == 1000, "clap starts the window")
steps, done = M.evaluate(trick, {
    emote = "RUDE", emote_target = "Innkeeper Farley",
    time = { ["local"] = { unix = 1020 } },
}, { trick = steps.trick }, false)
assertTrue(steps.trick.done and done, "rude within 30s finishes the step")
steps, done = M.evaluate(trick, {
    emote = "RUDE", emote_target = "Innkeeper Farley",
    time = { ["local"] = { unix = 1040 } },
}, { trick = { beat = 2, started_at = 1000 } }, false)
assertTrue(not steps.trick.done and steps.trick.failed and steps.trick.beat == 1 and (tonumber(steps.trick.remain) or 0) == 0, "late rude fails without ticking")
steps, done = M.evaluate(trick, {
    emote = "APPLAUD", emote_target = "", emote_text = "You clap for Brother Wilhelm.",
    time = { ["local"] = { unix = 1100 } },
}, { trick = { failed = true, beat = 1 } }, false)
assertTrue(not steps.trick.failed and steps.trick.beat == 2 and steps.trick.started_at == 1100 and steps.trick.remain == 30, "clapping again clears feilet")
steps, done = M.evaluate(trick, {
    emote = "APPLAUD", emote_target = "Brother Wilhelm",
    time = { ["local"] = { unix = 1010 } },
}, { trick = { beat = 2, started_at = 1000 } }, false)
assertTrue(not steps.trick.failed and steps.trick.beat == 2 and steps.trick.started_at == 1010 and steps.trick.remain == 30, "clapping again restarts the 30s")

-- group kill: exact level, attestations, no early latch.
local party = {
    instance = "The Deadmines",
    members = {
        { name = "Magni", level = 17 },
        { name = "Mira", level = 17 },
    },
}
local boss = {
    { id = "vancleef", type = "kill", text = "VanCleef", npc = "Edwin VanCleef",
      group = { level = 17, instance = "The Deadmines|Deadmines" } },
}
steps, done = M.evaluate(boss, {
    death_name = "Edwin VanCleef", now = 5000, group = party, attests = {},
}, {}, false)
assertTrue(not steps.vancleef.done and steps.vancleef.seen_at == 5000, "death alone does not latch")
steps, done = M.evaluate(boss, {
    now = 5005, group = party,
    attests = { vancleef = { magni = 5000, mira = 5004 } },
}, { vancleef = { seen_at = 5000 } }, false)
assertTrue(steps.vancleef.done and done, "both attestations within 15s")
local mixed = {
    instance = "The Deadmines",
    members = {
        { name = "Magni", level = 17 },
        { name = "Mira", level = 18 },
    },
}
steps, done = M.evaluate(boss, {
    now = 5005, group = mixed,
    attests = { vancleef = { magni = 5000, mira = 5004 } },
}, { vancleef = { seen_at = 5000 } }, false)
assertTrue(not steps.vancleef.done, "level 18 fails the exact gate")
steps, done = M.evaluate(boss, {
    now = 5020, group = party,
    attests = { vancleef = { magni = 5000, mira = 5004 } },
}, { vancleef = { seen_at = 5000 } }, false)
assertTrue(not steps.vancleef.done and (tonumber(steps.vancleef.seen_at) or 0) == 0, "witness expires")

-- level objective
steps, done = M.evaluate({
    { id = "ten", type = "level", text = "Bli 10", level = 10 },
}, { level = 9 }, {}, false)
assertTrue(not steps.ten.done and steps.ten.count == 9 and steps.ten.need == 10, "below target")
steps, done = M.evaluate({
    { id = "ten", type = "level", text = "Bli 10", level = 10 },
}, { level = 10 }, {}, false)
assertTrue(steps.ten.done and done, "at target")

-- instance_run: enter with group, leave → +1
local runs = { { id = "runs", type = "instance_run", text = "Runs", count = 2, group = { size = 2 } } }
local duo = { members = { { name = "Magni", level = 12 }, { name = "Mira", level = 12 } } }
steps, done = M.evaluate(runs, {
    instance_type = "party", instance_name = "The Deadmines", group = duo,
}, {}, false)
assertTrue(steps.runs.inside and steps.runs.count == 0 and not steps.runs.done, "inside first dungeon")
steps, done = M.evaluate(runs, {
    instance_type = "none", instance_name = "", group = duo,
}, { runs = { inside = true, count = 0 } }, false)
assertTrue(not steps.runs.inside and steps.runs.count == 1 and not steps.runs.done, "left counts once")
steps, done = M.evaluate(runs, {
    instance_type = "none", instance_name = "", group = duo,
}, { runs = { inside = true, count = 1 } }, false)
assertTrue(steps.runs.count == 2 and steps.runs.done and done, "second leave finishes")
steps, done = M.evaluate(runs, {
    instance_type = "party", instance_name = "The Deadmines",
    group = { members = { { name = "Magni", level = 12 } } },
}, { runs = { inside = true, count = 0 } }, false)
assertTrue(not steps.runs.inside and steps.runs.count == 0, "solo cancels stay")

assertTrue(M.goalHasProgress({ count = 1, need = 5 }) , "first rabbit is progress")
assertTrue(not M.goalHasProgress({ count = 0, need = 5, beat = 1 }), "untouched goal is not progress")
assertTrue(M.goalHasProgress({ done = true }), "a finished goal is progress")
local bucket = { steps = {} }
assertTrue(M.noteSagaStart(bucket, { rabbits = { count = 1, need = 5 } }, 1000), "first progress stamps the saga")
assertTrue(bucket.started_at == 1000, "stamp is the first event")
assertTrue(not M.noteSagaStart(bucket, { rabbits = { count = 2, need = 5 } }, 2000), "later progress keeps the stamp")
assertTrue(bucket.started_at == 1000, "stamp does not move")

print("objectives ok")
