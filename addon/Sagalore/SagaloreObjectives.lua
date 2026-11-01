-- Pure objective checks. No WoW API calls, so lua5.1 can test this file.
-- By default goals may complete in any order (ordered=false).
-- When ordered=true, step n is checked only after 1..n-1 are done.
-- state = { zone, subzone, map_id, x, y, npc, object, talk_name, items = { [id] = count },
--           kill_name, kill_secret,
--           emote, emote_target, emote_text, emote_zone, emote_subzone, emote_minimap,
--           emote_map_id, emote_x, emote_y,
--           time = { game = {hour, min}, ["local"] = {hour, min, unix}, realm = {hour, min, unix} } }
-- A talk completes on an open dialogue (npc / talk_name), a gossip-less interact
-- latched into talk_name, or emote TALK aimed at that npc (/talk). The current
-- target alone (object) is not a talk. Object steps still complete on target.
-- saved = { [objective_id] = { done = bool, count = number } }
--
-- all_of / any_of children are checked live (together), except kill counts which
-- accumulate. The composite itself stays done once it has been true.
-- memory visit latches. memory hold is true only while the world matches.
-- go/location default to visit. item/collect default to hold. Events are visit.
-- A sequence is one step: ordered beats, one within_sec window from the first beat.
-- A kill with group.level latches only when every member is that exact level
-- and has attested within 15 seconds of the death.
-- Clocks: game = GetGameTime, local = time()/date("*t"), realm = GetServerTime as UTC.

local M = {}

local function norm(s)
    if type(s) ~= "string" then
        return ""
    end
    return (s:lower():gsub("^%s+", ""):gsub("%s+$", ""))
end

local function same(a, b)
    local left = norm(a)
    return left ~= "" and left == norm(b)
end

local function needCount(obj)
    if type(obj) == "table" and obj.type == "level" then
        local n = tonumber(obj.level) or 0
        if n < 1 then
            return 0
        end
        return n
    end
    local n = tonumber(obj.count) or 1
    if n < 1 then
        return 1
    end
    return n
end

local function kindAllowed(obj, instanceType)
    local typ = tostring(instanceType or "")
    local kinds = obj.kinds
    if type(kinds) ~= "table" or #kinds == 0 then
        return typ == "party" or typ == "raid"
    end
    for i = 1, #kinds do
        if kinds[i] == typ then
            return true
        end
    end
    return false
end

local function playerXY(state)
    local x = tonumber(state.x)
    local y = tonumber(state.y)
    if not x or not y then
        return nil, nil
    end
    -- Some clients report map percents (0–100) instead of fractions.
    if x > 1 or y > 1 then
        x = x / 100
        y = y / 100
    end
    return x, y
end

local function onPoint(obj, state)
    if obj.x == nil or obj.y == nil then
        return false
    end
    local x, y = playerXY(state)
    local ox = tonumber(obj.x)
    local oy = tonumber(obj.y)
    if not x or not y or not ox or not oy then
        return false
    end
    local radius = tonumber(obj.radius) or 0.02
    local dx = x - ox
    local dy = y - oy
    return math.sqrt(dx * dx + dy * dy) <= radius
end

local function key(s)
    return (norm(s):gsub("[^%a%d]+", ""))
end

-- Split on "|" or ";" without a pattern. A "|" inside a Lua pattern is a
-- WoW escape and can make the matcher throw, which freezes later checks.
local function partsOf(want)
    local pipe = string.char(124)
    local out = {}
    local startAt = 1
    while startAt <= #want do
        local pipeAt = string.find(want, pipe, startAt, true)
        local semiAt = string.find(want, ";", startAt, true)
        local cut = pipeAt
        if semiAt and (not cut or semiAt < cut) then
            cut = semiAt
        end
        if not cut then
            table.insert(out, string.sub(want, startAt))
            break
        end
        table.insert(out, string.sub(want, startAt, cut - 1))
        startAt = cut + 1
    end
    return out
end

local function listed(want, got)
    if type(want) ~= "string" then
        return false
    end
    local gotKey = key(got)
    if want == "" or gotKey == "" then
        return false
    end
    if key(want) == gotKey then
        return true
    end
    local parts = partsOf(want)
    for i = 1, #parts do
        local partKey = key(parts[i])
        if partKey ~= "" and (partKey == gotKey or string.find(gotKey, partKey, 1, true) or string.find(partKey, gotKey, 1, true)) then
            return true
        end
    end
    return false
end

-- Soft group gate for instance_run: at least size members (default 2).
-- Optional group.instance filters the dungeon/raid name.
local function runGroupOK(rule, group, instanceName)
    if type(rule) ~= "table" then
        return true
    end
    local need = tonumber(rule.size) or 2
    if need < 2 then
        need = 2
    end
    local minLevel = 1
    local exact = tonumber(rule.level) or 0
    if exact > 0 then
        minLevel = exact
    elseif rule.min_level ~= nil then
        minLevel = tonumber(rule.min_level) or 1
    end
    group = type(group) == "table" and group or {}
    local members = type(group.members) == "table" and group.members or {}
    local n = 0
    for i = 1, #members do
        local m = members[i]
        if type(m) == "table" and norm(m.name) ~= "" and (tonumber(m.level) or 0) >= minLevel then
            n = n + 1
        end
    end
    if n < need then
        return false
    end
    local want = rule.instance
    if type(want) == "string" and want ~= "" then
        return listed(want, instanceName)
    end
    return true
end

local function placeHit(obj, state)
    -- obj.text is the line the player sees. It stays usable even when the
    -- subzone field never arrives in the loaded quest table.
    local wants = { obj.subzone, obj.zone, obj.text }
    local names = { state.subzone, state.zone, state.minimap }
    for i = 1, #names do
        for j = 1, #wants do
            if listed(wants[j], names[i]) then
                return true
            end
        end
    end
    return false
end

local function locationOK(obj, state)
    -- A coordinate hit counts even when Classic and retail disagree on map id.
    -- The minimap label counts too: indoors, GetSubZoneText is often empty
    -- while the minimap still says "Lion's Pride Inn".
    local here = onPoint(obj, state)
    if here or placeHit(obj, state) then
        return true
    end
    local zoneOnly = norm(obj.zone) ~= "" and obj.x == nil and obj.map_id == nil and norm(obj.subzone) == ""
    if zoneOnly and norm(obj.zone) == norm(state.zone) then
        return true
    end
    if obj.map_id ~= nil and obj.x == nil and norm(obj.subzone) == "" and norm(obj.zone) == "" then
        return tonumber(obj.map_id) == tonumber(state.map_id)
    end
    return false
end

local function emoteToken(s)
    s = norm(s)
    if s:sub(1, 1) == "/" then
        s = s:sub(2)
    end
    s = (s:gsub("%s+", "_"):upper())
    -- Classic /clap is CLAP; sagas usually ask for APPLAUD.
    if s == "CLAP" then
        return "APPLAUD"
    end
    return s
end

-- Same target match as an emote step: the unit name, or the name in the chat line.
local function emoteAimedAt(want, state)
    if norm(want) == "" then
        return false
    end
    return same(want, state.emote_target) or listed(want, state.emote_target) or listed(want, state.emote_text)
end

-- /talk while the named unit is targeted. Gossip-less NPCs never open a frame.
local function talkEmoteOK(obj, state)
    if emoteToken(state.emote) ~= "TALK" then
        return false
    end
    return emoteAimedAt(obj.npc, state) or emoteAimedAt(obj.object, state)
end

local function interactOK(obj, state)
    local wantNPC = norm(obj.npc) ~= ""
    local wantObject = norm(obj.object) ~= ""
    if not wantNPC and not wantObject then
        return false
    end
    -- state.npc is the open dialogue unit (gossip / quest / merchant / trainer).
    -- talk_name is that dialogue, or a gossip-less interact (right-click /
    -- InteractUnit) latched by the addon. Targeting alone is not a talk.
    -- state.object is the current target and completes object steps only.
    -- /talk reuses the emote token and target (CHAT_MSG_TEXT_EMOTE / DoEmote).
    if talkEmoteOK(obj, state) then
        return true
    end
    local talked = wantNPC and (same(obj.npc, state.npc) or listed(obj.npc, state.npc)
        or same(obj.npc, state.talk_name) or listed(obj.npc, state.talk_name))
    local object = wantObject and (same(obj.object, state.object) or same(obj.object, state.npc)
        or listed(obj.object, state.object) or listed(obj.object, state.npc))
    if wantNPC and wantObject then
        return talked or object
    end
    if wantNPC then
        return talked
    end
    return object
end

local function itemHave(state, id)
    if type(state.items) ~= "table" or id == nil then
        return 0
    end
    local n = state.items[id]
    if n == nil then
        n = state.items[tostring(id)]
    end
    return tonumber(n) or 0
end

local function hasPlace(obj)
    return norm(obj.zone) ~= "" or norm(obj.subzone) ~= "" or obj.map_id ~= nil or obj.x ~= nil or obj.y ~= nil
end

local function emoteOK(obj, state)
    local want = emoteToken(obj.emote)
    local got = emoteToken(state.emote)
    if want == "" or got == "" or want ~= got then
        return false
    end
    if norm(obj.npc) ~= "" and not emoteAimedAt(obj.npc, state) then
        return false
    end
    if not hasPlace(obj) then
        return true
    end
    return locationOK(obj, {
        zone = state.emote_zone,
        subzone = state.emote_subzone,
        minimap = state.emote_minimap,
        map_id = state.emote_map_id,
        x = state.emote_x,
        y = state.emote_y,
    })
end

local function isLeap(y)
    return (y % 4 == 0 and y % 100 ~= 0) or (y % 400 == 0)
end

local function monthDays(y, m)
    local mdays = { 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31 }
    if m == 2 and isLeap(y) then
        return 29
    end
    return mdays[m] or 0
end

local function unixUTC(y, m, d, hour, minute, second)
    if m < 1 or m > 12 or d < 1 or d > monthDays(y, m) then
        return nil
    end
    local days = 0
    for year = 1970, y - 1 do
        days = days + (isLeap(year) and 366 or 365)
    end
    for month = 1, m - 1 do
        days = days + monthDays(y, month)
    end
    days = days + d - 1
    return days * 86400 + hour * 3600 + minute * 60 + second
end

local function parseRFC3339(s)
    if type(s) ~= "string" then
        return nil
    end
    local y, mo, d, H, Mi, S, rest = s:match("^(%d%d%d%d)%-(%d%d)%-(%d%d)T(%d%d):(%d%d):(%d%d)(.*)$")
    if not y then
        return nil
    end
    rest = rest:gsub("^%.%d+", "")
    local offset = 0
    if rest ~= "Z" and rest ~= "" then
        local sign, oh, om = rest:match("^([%+%-])(%d%d):(%d%d)$")
        if not sign then
            return nil
        end
        offset = (tonumber(oh) * 60 + tonumber(om)) * 60
        if sign == "-" then
            offset = -offset
        end
    end
    local unix = unixUTC(tonumber(y), tonumber(mo), tonumber(d), tonumber(H), tonumber(Mi), tonumber(S))
    if not unix then
        return nil
    end
    return unix - offset
end

local function minutes(s)
    if type(s) ~= "string" then
        return nil
    end
    local h, m = s:match("^(%d%d):(%d%d)$")
    h, m = tonumber(h), tonumber(m)
    if not h or not m then
        return nil
    end
    return h * 60 + m
end

local function inWindow(hour, min, startAt, endAt)
    hour, min = tonumber(hour), tonumber(min)
    local cur = hour and min and (hour * 60 + min) or nil
    local a, b = minutes(startAt), minutes(endAt)
    if not cur or not a or not b then
        return false
    end
    if a <= b then
        return cur >= a and cur <= b
    end
    return cur >= a or cur <= b
end

-- timeOpen is true when there is no window, or the state's clock matches.
-- A missing clock fails closed so a saga cannot complete on the wrong clock.
function M.timeOpen(when, state)
    if type(when) ~= "table" then
        return true
    end
    local clock = when.clock
    if type(clock) ~= "string" or clock == "" then
        clock = "local"
    end
    local clocks = type(state) == "table" and state.time or nil
    local now = type(clocks) == "table" and clocks[clock] or nil
    if type(now) ~= "table" then
        return false
    end
    if type(when.start) == "string" and when.start ~= "" then
        if not inWindow(now.hour, now.min, when.start, when["end"]) then
            return false
        end
    end
    local unix = tonumber(now.unix)
    if type(when.starts_at) == "string" and when.starts_at ~= "" then
        local t = parseRFC3339(when.starts_at)
        if not t or not unix or unix < t then
            return false
        end
    end
    if type(when.ends_at) == "string" and when.ends_at ~= "" then
        local t = parseRFC3339(when.ends_at)
        if not t or not unix or unix > t then
            return false
        end
    end
    return true
end

local function pluralEN(n, one, many)
    if n == 1 then
        return "1 " .. one
    end
    return tostring(n) .. " " .. many
end

-- Human relative span for absolute deadlines (English, matching Level labels).
local function formatSpan(sec)
    if sec < 0 then
        sec = -sec
    end
    local days = math.floor(sec / 86400)
    local hours = math.floor((sec % 86400) / 3600)
    local mins = math.floor((sec % 3600) / 60)
    if days >= 7 then
        return pluralEN(days, "day", "days")
    end
    if days >= 1 then
        local parts = { pluralEN(days, "day", "days") }
        if hours > 0 and days < 3 then
            parts[#parts + 1] = pluralEN(hours, "hour", "hours")
        end
        return table.concat(parts, " and ")
    end
    if hours >= 1 then
        local parts = { pluralEN(hours, "hour", "hours") }
        if mins > 0 then
            parts[#parts + 1] = pluralEN(mins, "minute", "minutes")
        end
        return table.concat(parts, " and ")
    end
    if mins < 1 then
        return "under a minute"
    end
    return pluralEN(mins, "minute", "minutes")
end

local function nowUnixForHint(when, state)
    local clock = when.clock
    if type(clock) ~= "string" or clock == "" then
        clock = "local"
    end
    local clocks = type(state) == "table" and state.time or nil
    local now = type(clocks) == "table" and clocks[clock] or nil
    if type(now) ~= "table" then
        now = type(clocks) == "table" and clocks["local"] or nil
    end
    if type(now) ~= "table" then
        return nil
    end
    return tonumber(now.unix)
end

-- timeHint is the parenthetical shown after a goal line. Absolute dates become
-- relative English ("in 3 hours and 23 minutes"). Emote tokens are not added here.
function M.timeHint(when, state, done)
    if type(when) ~= "table" then
        return ""
    end
    local bits = {}
    if type(when.start) == "string" and when.start ~= "" and type(when["end"]) == "string" and when["end"] ~= "" then
        bits[#bits + 1] = when.start .. "–" .. when["end"]
    end
    local unix = nowUnixForHint(when, state)
    if not done and unix then
        local startAt = type(when.starts_at) == "string" and when.starts_at ~= "" and parseRFC3339(when.starts_at) or nil
        local endAt = type(when.ends_at) == "string" and when.ends_at ~= "" and parseRFC3339(when.ends_at) or nil
        if startAt and unix < startAt then
            bits[#bits + 1] = "in " .. formatSpan(startAt - unix)
        elseif endAt then
            if unix > endAt then
                bits[#bits + 1] = "expired"
            else
                bits[#bits + 1] = "in " .. formatSpan(endAt - unix)
            end
        end
    elseif not done then
        -- No unix on this clock (e.g. game clock) — fall back to raw ends_at only if present.
        if type(when.ends_at) == "string" and when.ends_at ~= "" then
            bits[#bits + 1] = "until " .. when.ends_at
        elseif type(when.starts_at) == "string" and when.starts_at ~= "" then
            bits[#bits + 1] = "from " .. when.starts_at
        end
    end
    if #bits == 0 then
        return ""
    end
    local text = " (" .. table.concat(bits, ", ") .. ")"
    if not done and not M.timeOpen(when, state) then
        if bits[1] == "expired" then
            return text
        end
        return " — waiting" .. text
    end
    return text
end

-- groupOK is the saga-level party check. A missing rule is always ok.
-- members below min_level do not count. instance matches the dungeon name or the zone.
function M.groupOK(rule, group)
    if type(rule) ~= "table" or tonumber(rule.size) == nil then
        return true
    end
    local need = tonumber(rule.size) or 1
    if need < 1 then
        return true
    end
    local minLevel = tonumber(rule.min_level) or 1
    group = type(group) == "table" and group or {}
    local n = 0
    local members = type(group.members) == "table" and group.members or {}
    for i = 1, #members do
        local m = members[i]
        if type(m) == "table" and norm(m.name) ~= "" and (tonumber(m.level) or 0) >= minLevel then
            n = n + 1
        end
    end
    if n < need then
        return false
    end
    if norm(rule.instance) ~= "" then
        if not (listed(rule.instance, group.instance) or listed(rule.instance, group.zone)) then
            return false
        end
    end
    return true
end

local function preserveTree(obj, saved, into)
    if type(obj) ~= "table" or type(obj.objectives) ~= "table" then
        return
    end
    for i, child in ipairs(obj.objectives) do
        if type(child) == "table" then
            local id = child.id or tostring(i)
            if type(saved[id]) == "table" then
                into[id] = saved[id]
            end
            preserveTree(child, saved, into)
        end
    end
end

local attestWindow = 15

local function memoryOf(obj)
    local m = obj.memory
    if m == "hold" or m == "visit" then
        return m
    end
    local k = obj.type
    if k == "item" or k == "collect" then
        return "hold"
    end
    return "visit"
end

local function stepKind(kind)
    if kind == "go" then
        return "location"
    end
    if kind == "talk" then
        return "interact"
    end
    if kind == "item" then
        return "collect"
    end
    return kind
end

local function nowUnix(state)
    local n = tonumber(state.now)
    if n and n > 0 then
        return n
    end
    local clocks = state.time
    if type(clocks) == "table" and type(clocks["local"]) == "table" then
        n = tonumber(clocks["local"].unix)
        if n and n > 0 then
            return n
        end
    end
    return 0
end

-- groupGate is the kill-step party check. level is exact. size, when set, is
-- the exact roster length. Every member must have an attest timestamp within
-- attestWindow seconds of now. A missing rule fails closed.
function M.groupGate(rule, group, attests, now)
    if type(rule) ~= "table" or tonumber(rule.level) == nil then
        return false
    end
    local level = tonumber(rule.level) or 0
    if level < 1 then
        return false
    end
    group = type(group) == "table" and group or {}
    local members = type(group.members) == "table" and group.members or {}
    local names = {}
    for i = 1, #members do
        local m = members[i]
        if type(m) == "table" and norm(m.name) ~= "" then
            if (tonumber(m.level) or 0) ~= level then
                return false
            end
            names[#names + 1] = norm(m.name)
        end
    end
    local size = tonumber(rule.size)
    if size and size > 0 then
        if #names ~= size then
            return false
        end
    elseif #names < 2 then
        return false
    end
    if norm(rule.instance) ~= "" then
        if not (listed(rule.instance, group.instance) or listed(rule.instance, group.zone)) then
            return false
        end
    end
    now = tonumber(now) or 0
    if now <= 0 then
        return false
    end
    attests = type(attests) == "table" and attests or {}
    for i = 1, #names do
        local at = tonumber(attests[names[i]]) or 0
        if at <= 0 or now < at or now - at > attestWindow then
            return false
        end
    end
    return true
end

local function beatOK(beat, state)
    if type(beat) ~= "table" then
        return false
    end
    local kind = stepKind(beat.type)
    if kind == "location" then
        return locationOK(beat, state)
    end
    if kind == "interact" then
        return interactOK(beat, state)
    end
    if kind == "emote" then
        return emoteOK(beat, state)
    end
    if kind == "kill" then
        if state.kill_secret then
            return false
        end
        return listed(beat.npc, state.kill_name) or listed(beat.npc, state.death_name)
    end
    return false
end

local function eventBeat(beat)
    if type(beat) ~= "table" then
        return false
    end
    local kind = stepKind(beat.type)
    return kind == "emote" or kind == "interact" or kind == "kill"
end

local function checkSequence(obj, state, prev)
    local beats = type(obj.beats) == "table" and obj.beats or {}
    local n = #beats
    local now = nowUnix(state)
    local window = tonumber(obj.within_sec) or 30
    local beat = tonumber(prev.beat) or 1
    local started = tonumber(prev.started_at) or 0
    local failed = prev.failed and true or false
    local consumed = false
    if beat < 1 or beat > n then
        beat = 1
    end
    if not failed and started > 0 and now > 0 and now > started + window then
        failed = true
        beat = 1
        started = 0
    end
    if failed then
        if beats[1] and beatOK(beats[1], state) then
            failed = false
            started = now
            beat = 2
            consumed = true
        end
    else
        local cur = beats[beat]
        if beat > 1 and eventBeat(beats[1]) and beatOK(beats[1], state) and not (cur and beatOK(cur, state)) then
            started = now
            beat = 2
            consumed = true
        elseif cur and beatOK(cur, state) then
            consumed = true
            if beat == 1 then
                started = now
            end
            beat = beat + 1
        end
    end
    local done = (not failed) and n > 0 and beat > n
    if done then
        failed = false
        started = 0
    end
    local remain = 0
    if not done and not failed and started > 0 and now > 0 then
        remain = started + window - now
        if remain < 0 then
            remain = 0
        end
    end
    return {
        done = done,
        failed = failed,
        count = done and n or (beat - 1),
        need = n,
        active = true,
        beat = done and n or beat,
        started_at = done and 0 or started,
        remain = remain,
        consumed_emote = consumed,
    }
end

local checkOne

local function checkComposite(obj, state, saved)
    local rows = {}
    local all, any = true, false
    local have, total = 0, 0
    local kids = type(obj.objectives) == "table" and obj.objectives or {}
    for i, child in ipairs(kids) do
        if type(child) == "table" then
            total = total + 1
            local id = child.id or tostring(i)
            local prev = saved[id] or {}
            local row, nested = checkOne(child, state, saved, prev, true)
            rows[id] = row
            if type(nested) == "table" then
                for cid, crow in pairs(nested) do
                    rows[cid] = crow
                end
            end
            if row.done then
                have = have + 1
                any = true
            else
                all = false
            end
        end
    end
    local done = false
    if obj.type == "any_of" then
        done = any
    elseif total > 0 then
        done = all
    end
    return done, rows, have, total
end

checkOne = function(obj, state, saved, prev, live)
    local memory = memoryOf(obj)
    local row = {
        done = prev.done and true or false,
        count = tonumber(prev.count) or 0,
        need = needCount(obj),
        active = false,
        beat = tonumber(prev.beat) or 0,
        started_at = tonumber(prev.started_at) or 0,
        seen_at = tonumber(prev.seen_at) or 0,
        remain = tonumber(prev.remain) or 0,
        failed = prev.failed and true or false,
        inside = prev.inside and true or false,
    }
    -- visit latches. hold is recomputed every check, including inside a composite.
    if memory ~= "hold" and not live and row.done then
        local kept = {}
        preserveTree(obj, saved, kept)
        return row, kept
    end
    if live or memory == "hold" then
        row.done = false
    end
    row.active = true
    local open = M.timeOpen(obj.when, state)
    local nested
    local kind = stepKind(obj.type)
    if kind == "location" then
        row.done = open and locationOK(obj, state)
    elseif kind == "interact" then
        row.done = open and interactOK(obj, state)
    elseif kind == "collect" then
        row.count = itemHave(state, obj.item_id)
        row.done = open and row.count >= row.need
    elseif kind == "kill" and type(obj.group) == "table" and obj.group.level ~= nil then
        local now = nowUnix(state)
        local seen = tonumber(prev.seen_at) or 0
        local death = state.death_name
        if type(death) ~= "string" or death == "" then
            death = state.kill_name
        end
        if not state.kill_secret and listed(obj.npc, death) and now > 0 then
            seen = now
        end
        if seen > 0 and now > seen + attestWindow then
            seen = 0
        end
        row.seen_at = seen
        local attests = {}
        if type(state.attests) == "table" and type(state.attests[obj.id]) == "table" then
            attests = state.attests[obj.id]
        end
        row.done = open and seen > 0 and M.groupGate(obj.group, state.group, attests, now)
        row.count = 0
        row.need = tonumber(obj.group.size) or 0
    elseif kind == "kill" then
        if state.kill_secret then
            row.count = tonumber(prev.count) or 0
        elseif listed(obj.npc, state.kill_name) then
            row.count = (tonumber(prev.count) or 0) + 1
        else
            row.count = tonumber(prev.count) or 0
        end
        row.done = open and row.count >= row.need
    elseif kind == "emote" then
        -- Sticky once matched: pendingEmote is only live for a few seconds, and
        -- finishQuest / UI refresh may land on a later tick.
        if open and emoteOK(obj, state) then
            row.done = true
        elseif memory ~= "hold" and not live and prev.done then
            row.done = true
        else
            row.done = false
        end
    elseif kind == "level" then
        local have = tonumber(state.level) or 0
        row.count = have
        row.need = needCount(obj)
        row.done = open and row.need > 0 and have >= row.need
    elseif kind == "instance_run" then
        local count = tonumber(prev.count) or 0
        local inside = prev.inside and true or false
        local typ = tostring(state.instance_type or "none")
        local name = state.instance_name or ""
        local matching = kindAllowed(obj, typ)
        local grouped = runGroupOK(obj.group, state.group, name)
        if open and matching and grouped then
            inside = true
        elseif inside and open and (typ == "none" or typ == "") then
            count = count + 1
            inside = false
        elseif inside and matching and not grouped then
            -- Dropped the group mid-run — this stay does not count.
            inside = false
        elseif typ ~= "none" and typ ~= "" and not matching then
            -- Entered something that is not a counted kind (e.g. arena).
            inside = false
        end
        row.count = count
        row.need = needCount(obj)
        row.inside = inside
        row.done = open and count >= row.need
    elseif obj.type == "sequence" then
        local seq = checkSequence(obj, state, prev)
        row.done = open and seq.done
        row.count = seq.count
        row.need = seq.need
        row.beat = seq.beat
        row.started_at = seq.started_at
        row.remain = seq.remain
        row.failed = seq.failed and true or false
        row.consumed_emote = seq.consumed_emote and true or false
    elseif kind == "all_of" or kind == "any_of" or obj.type == "all_of" or obj.type == "any_of" then
        local done, rows, have, total = checkComposite(obj, state, saved)
        nested = rows
        row.count = have
        row.need = total
        row.done = open and done
    else
        row.done = false
    end
    if not open and (memory == "hold" or not (prev.done and not live)) then
        row.done = false
    end
    return row, nested
end

-- ordered: when true (or omitted for backwards-compatible tests), gate later
-- goals on earlier ones. Pass false for any-order sagas.
-- sagaWhen: optional saga-level window. While it is closed, nothing new completes.
function M.evaluate(objectives, state, saved, ordered, sagaWhen)
    state = state or {}
    saved = saved or {}
    if ordered == nil then
        ordered = true
    end
    local sagaOpen = M.timeOpen(sagaWhen, state)
    local steps = {}
    local prevDone = true
    local allDone = type(objectives) == "table" and #objectives > 0
    if type(objectives) ~= "table" then
        return steps, false
    end
    for i, obj in ipairs(objectives) do
        if type(obj) ~= "table" then
            allDone = false
            prevDone = false
        else
            local id = obj.id or tostring(i)
            local prev = saved[id] or {}
            local row, nested
            if ordered and not prevDone and not (prev.done and true or false) then
                row = {
                    done = false,
                    count = tonumber(prev.count) or 0,
                    need = needCount(obj),
                    active = false,
                }
                nested = {}
                preserveTree(obj, saved, nested)
                allDone = false
            else
                row, nested = checkOne(obj, state, saved, prev, false)
                if not sagaOpen and not (prev.done and true or false) then
                    row.done = false
                end
                if row.done then
                    prevDone = true
                else
                    prevDone = false
                    allDone = false
                end
            end
            steps[id] = row
            if type(nested) == "table" then
                for cid, crow in pairs(nested) do
                    steps[cid] = crow
                end
            end
        end
    end
    if not sagaOpen then
        allDone = false
    end
    return steps, allDone
end

-- goalHasProgress is true once a goal has moved past "not started".
-- A counted kill counts at the first credit, not when the count is full.
function M.goalHasProgress(row)
    if type(row) ~= "table" then
        return false
    end
    if row.done or row.failed or row.inside then
        return true
    end
    if (tonumber(row.count) or 0) > 0 then
        return true
    end
    if (tonumber(row.beat) or 0) > 1 then
        return true
    end
    if (tonumber(row.seen_at) or 0) > 0 then
        return true
    end
    return false
end

-- noteSagaStart stamps bucket.started_at the first time any goal has progress.
-- Later progress does not move the stamp. Returns true when it writes one.
function M.noteSagaStart(bucket, steps, now)
    if type(bucket) ~= "table" then
        return false
    end
    if (tonumber(bucket.started_at) or 0) > 0 then
        return false
    end
    if type(steps) ~= "table" then
        return false
    end
    local stamped = false
    for _, row in pairs(steps) do
        if M.goalHasProgress(row) then
            stamped = true
            break
        end
    end
    if not stamped then
        return false
    end
    local at = tonumber(now) or 0
    if at < 1 then
        return false
    end
    bucket.started_at = at
    return true
end

M.listed = listed
SagaloreObjectives = M
return M
