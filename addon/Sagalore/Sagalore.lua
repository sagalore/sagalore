-- Sagalore quest log for Forever, retail Midnight, Classic Era, and Classic (Mists).
-- Interface numbers live in the .toc files (see addon/INTERFACE).
-- Completions are stored in SagaloreDB (SavedVariables). On Classic Era the
-- companion reads those after /reload or logout — live chat-log signaling is parked.
-- There is no completion button. Goals may complete in any order unless
-- objectives_ordered is true on the saga.

local PREFIX = "Sagalore"
-- Same default as internal/app DefaultCreateSagasURL (copyable EditBox in the log).
local CREATE_SAGAS_URL = "https://sagalore.app/#create"

local function L(key)
    if type(Sagalore_L) == "function" then
        local value = Sagalore_L(key)
        if type(value) == "string" and value ~= "" then
            return value
        end
    end
    return key
end
local player = { name = "", realm = "", level = 1, race = "", class = "", battletag = "" }
local world = {
    zone = "", subzone = "", minimap = "", mapID = nil, x = nil, y = nil,
    npc = "", object = "", killName = nil, killSecret = false, deathName = nil, attests = {},
    gameHour = nil, gameMin = nil, instanceName = "", groupSnap = nil, pendingEmote = nil,
}
local readClocks
local readGroup
local frame
local toast
local showSagaToast = function() end
local sectionOpen
local sectionDone
local listPanel
local detailPanel
-- Color tables are packed so Classic Era stays under Lua 5.1's 200-local limit
-- for the main chunk (FrameXML: "main function has more than 200 local variables").
local GOLD = { r = 0.99, g = 0.93, b = 0.59 }
local LOCKED_TITLE = { r = 0.45, g = 0.42, b = 0.38 }
local LOCKED_SUB = { r = 0.40, g = 0.37, b = 0.34 }
-- Fallback difficulty colors when GetQuestDifficultyColor is missing.
local DIFF = {
    YELLOW = { r = 1.00, g = 1.00, b = 0.00 },
    RED = { r = 1.00, g = 0.10, b = 0.10 },
    ORANGE = { r = 1.00, g = 0.50, b = 0.25 },
    GREEN = { r = 0.25, g = 0.75, b = 0.25 },
    GRAY = { r = 0.50, g = 0.50, b = 0.50 },
}
local listChild
local listScroll
local detailFS
local detailImage
local detailTitle
local detailMeta
local detailGoalsHeader
local detailActivityHeader
local detailChild
local detailScroll
local rows = {}
local goalRows = {}
local activityRows = {}
local INK = { r = 0.22, g = 0.12, b = 0.04 }
local INK_MUTED = { r = 0.42, g = 0.35, b = 0.25 }
local lastDetailID
local selectedID
-- Toast (and similar) can open a completed saga while every saga is done.
-- show() clears completed selection on open so the all-done info screen can appear;
-- this flag keeps an explicit completed pick.
local preserveCompletedSelection = false
local built = false
local checking = false
local lastCharacterLine = ""
local syncScrollBar
local scrollBy
local bindWheel
local wheelOnFrame

local function secret(v)
    if issecretvalue == nil or v == nil then
        return false
    end
    local ok, isSecret = pcall(issecretvalue, v)
    return ok and isSecret and true or false
end

local function plainString(v)
    if type(v) ~= "string" or secret(v) or v == "" then
        return nil
    end
    return v
end

local function plainNumber(v)
    if type(v) ~= "number" or secret(v) then
        return nil
    end
    return v
end

local function same(a, b)
    if type(a) ~= "string" or type(b) ~= "string" then
        return false
    end
    return a:lower() == b:lower()
end

local function norm(s)
    if type(s) ~= "string" then
        return ""
    end
    return (s:lower():gsub("^%s+", ""):gsub("%s+$", ""))
end

local function unixNow()
    if GetServerTime then
        local ok, v = pcall(GetServerTime)
        local n = ok and plainNumber(v) or nil
        if n and n > 0 then
            return math.floor(n)
        end
    end
    return time()
end

-- Machine COMPLETE/CHARACTER/LEVELUP lines are parked on Classic Era: chat
-- logging does not reliably accept addon SendChatMessage (taint / no flush).
-- Completions go to SagaloreDB and reach Discord after /reload via the companion.
local function chat(line)
    -- Machine lines ([Sagalore] CHARACTER / COMPLETE / LEVELUP / INVITE) must
    -- reach the chat log so the companion can post them. The addon has no HTTP.
    if DEFAULT_CHAT_FRAME and DEFAULT_CHAT_FRAME.AddMessage then
        DEFAULT_CHAT_FRAME:AddMessage(line)
    else
        print(line)
    end
end

local function registerPrefix(prefix)
    if C_ChatInfo and C_ChatInfo.RegisterAddonMessagePrefix then
        pcall(C_ChatInfo.RegisterAddonMessagePrefix, prefix)
    elseif RegisterAddonMessagePrefix then
        pcall(RegisterAddonMessagePrefix, prefix)
    end
end

local function sendAddon(prefix, message, channel)
    if C_ChatInfo and C_ChatInfo.SendAddonMessage then
        pcall(C_ChatInfo.SendAddonMessage, prefix, message, channel)
    elseif SendAddonMessage then
        pcall(SendAddonMessage, prefix, message, channel)
    end
end

local function initDB()
    if type(SagaloreDB) ~= "table" then
        SagaloreDB = {}
    end
    if type(SagaloreDB.completions) ~= "table" then
        SagaloreDB.completions = {}
    end
    if type(SagaloreDB.objectives) ~= "table" then
        SagaloreDB.objectives = {}
    end
    if type(SagaloreDB.pending_resets) ~= "table" then
        SagaloreDB.pending_resets = {}
    end
    if type(SagaloreDB.read) ~= "table" then
        SagaloreDB.read = {}
    end
end

local minimapBtn
local updateMinimapUnread

-- Queue an API progress wipe for the companion (after /reload writes SV).
-- Without this, the next sync restores completed status and local reset looks broken.
local function queueReset(questID)
    initDB()
    local row = { at = unixNow() }
    if questID and questID ~= "" then
        row.saga_id = questID
    else
        row.all = true
    end
    table.insert(SagaloreDB.pending_resets, row)
end

-- SagaloreData is only read at login and /reload. The companion writes notice
-- into that file when a saga changed, and shows the "skriv /reload" banner
-- on the desktop because it cannot type into the client. After /reload this
-- prints the notice once per character.
local function showFreshNotice()
    if type(SagaloreData) ~= "table" then
        return
    end
    local id = SagaloreData.notice_id
    local text = SagaloreData.notice
    if type(id) ~= "string" or id == "" or type(text) ~= "string" or text == "" then
        return
    end
    initDB()
    if SagaloreDB.seen_notice == id then
        return
    end
    SagaloreDB.seen_notice = id
    chat("|cffc9a227Sagalore|r: " .. text)
end

local function pipe(s)
    s = s or ""
    s = s:gsub("|", "/")
    s = s:gsub("%s+", " ")
    s = (s:gsub("^%s+", ""):gsub("%s+$", ""))
    if s == "" then
        return "-"
    end
    return s
end

local function readBattleTag()
    if not BNGetInfo then
        return ""
    end
    local results = { pcall(BNGetInfo) }
    if not results[1] then
        return ""
    end
    for i = 2, #results do
        local s = plainString(results[i])
        if s and s:find("#", 1, true) then
            return s
        end
    end
    return ""
end

local function unitLabel(fn)
    if not fn then
        return ""
    end
    -- Prefer the localized display name. UnitClass's second return is the
    -- English file token (HUNTER), which looks wrong in Discord and the UI.
    local ok, localized, english = pcall(fn, "player")
    if not ok then
        return ""
    end
    local loc = plainString(localized)
    if loc then
        return loc
    end
    return plainString(english) or ""
end

local function cachePlayer()
    if InCombatLockdown and InCombatLockdown() then
        return
    end
    if UnitFullName then
        local ok, name, realm = pcall(UnitFullName, "player")
        if ok then
            local n = plainString(name)
            if n then
                player.name = n
            end
            local r = plainString(realm)
            if r then
                player.realm = r
            end
        end
    end
    if player.realm == "" and GetRealmName then
        local ok, realm = pcall(GetRealmName)
        if ok then
            local r = plainString(realm)
            if r then
                player.realm = r
            end
        end
    end
    if UnitLevel then
        local ok, level = pcall(UnitLevel, "player")
        if ok then
            local n = plainNumber(level)
            if n and n > 0 then
                player.level = n
            end
        end
    end
    local race = unitLabel(UnitRace)
    if race ~= "" then
        player.race = race
    end
    local className = unitLabel(UnitClass)
    if className ~= "" then
        player.class = className
    end
    local tag = readBattleTag()
    if tag ~= "" then
        player.battletag = tag
    end
end

local function emitCharacter()
    if player.name == "" or player.level < 1 then
        return
    end
    local line = string.format(
        "[Sagalore] CHARACTER %s|%s|%d|%s|%s|%s|%d",
        pipe(player.name), pipe(player.realm), player.level,
        pipe(player.race), pipe(player.class), pipe(player.battletag), unixNow()
    )
    if line == lastCharacterLine then
        return
    end
    lastCharacterLine = line
    chat(line)
    -- Classic Era chat logging does not capture AddMessage. Queue the same
    -- snapshot for the companion via SavedVariables (flushed on /reload).
    initDB()
    SagaloreDB.pending_character = {
        name = player.name,
        realm = player.realm or "",
        level = player.level,
        race = player.race or "",
        class = player.class or "",
        battletag = player.battletag or "",
        at = unixNow(),
    }
end

local function isComplete(entry)
    if type(entry) ~= "table" then
        return false
    end
    if entry.status == "completed" then
        return true
    end
    local id = entry.saga_id
    return type(id) == "string" and type(SagaloreDB) == "table"
        and type(SagaloreDB.completions) == "table" and SagaloreDB.completions[id] ~= nil
end

-- WoW-style quest-log colors from saga min_level vs player level.
-- No min_level → yellow. Far below → red, a bit below → orange,
-- around / slight above → yellow, a bit above → green, a lot above → gray.
local function difficultyColor(minLevel, playerLevel)
    local level = tonumber(minLevel)
    if not level or level < 1 then
        return DIFF.YELLOW
    end
    local pl = tonumber(playerLevel) or 1
    if GetQuestDifficultyColor then
        local ok, c = pcall(GetQuestDifficultyColor, level)
        if ok and type(c) == "table" and type(c.r) == "number" then
            return c
        end
    end
    local diff = level - pl
    if diff >= 5 then
        return DIFF.RED
    elseif diff >= 3 then
        return DIFF.ORANGE
    elseif diff >= -2 then
        return DIFF.YELLOW
    end
    local greenRange = 5
    if GetQuestGreenRange then
        local ok, n = pcall(GetQuestGreenRange)
        if ok and type(n) == "number" and n > 0 then
            greenRange = n
        end
    end
    if -diff <= greenRange then
        return DIFF.GREEN
    end
    return DIFF.GRAY
end

local function currentEntries()
    local out = {}
    if player.name == "" or type(SagaloreData) ~= "table" or type(SagaloreData.entries) ~= "table" then
        return out
    end
    for _, entry in ipairs(SagaloreData.entries) do
        if type(entry) == "table" then
            -- "*" is a provisional sync row before the companion has upserted this character.
            local forMe = same(entry.character, player.name) or entry.character == "*"
            if forMe then
                local realm = entry.realm
                if type(realm) ~= "string" or realm == "" or player.realm == "" or same(realm, player.realm) or entry.character == "*" then
                    local visible = true
                    if type(SagaloreInvite) == "table" and type(SagaloreInvite.entryVisible) == "function" then
                        visible = SagaloreInvite.entryVisible(SagaloreDB, entry)
                    end
                    if visible then
                        table.insert(out, entry)
                    end
                end
            end
        end
    end
    table.sort(out, function(a, b)
        local ac = isComplete(a) and 1 or 0
        local bc = isComplete(b) and 1 or 0
        if ac ~= bc then
            return ac < bc
        end
        local achain = (type(a.chain_id) == "string" and a.chain_id ~= "") and 0 or 1
        local bchain = (type(b.chain_id) == "string" and b.chain_id ~= "") and 0 or 1
        if achain ~= bchain then
            return achain < bchain
        end
        local an = a.chain_name or a.chain_id or ""
        local bn = b.chain_name or b.chain_id or ""
        if an ~= bn then
            return an < bn
        end
        local ao = tonumber(a.chain_order) or 0
        local bo = tonumber(b.chain_order) or 0
        if ao ~= bo then
            return ao < bo
        end
        local am = tonumber(a.min_level) or 0
        local bm = tonumber(b.min_level) or 0
        if am ~= bm then
            return am < bm
        end
        return (a.title or "") < (b.title or "")
    end)
    return out
end

local function findEntry(id)
    local entries = currentEntries()
    for i = 1, #entries do
        if entries[i].saga_id == id then
            return entries[i]
        end
    end
    return nil
end

-- SagaloreDB is SavedVariablesPerCharacter, so read state is already per toon.
-- Flat map: read[saga_id] = true. read_bootstrapped means the one-time
-- upgrade seed already ran. Incomplete sagas missing from read are unread.
local function flattenReadMap(raw)
    local flat = {}
    if type(raw) ~= "table" then
        return flat
    end
    for k, v in pairs(raw) do
        if type(v) == "table" then
            -- Old name\nrealm nesting from the first unread build.
            for qid, flag in pairs(v) do
                if flag and type(qid) == "string" then
                    flat[qid] = true
                end
            end
        elseif v == true and type(k) == "string" then
            flat[k] = true
        end
    end
    return flat
end

local function ensureReadMap()
    initDB()
    if SagaloreDB.read_bootstrapped then
        SagaloreDB.read = flattenReadMap(SagaloreDB.read)
        return SagaloreDB.read
    end

    local flat = flattenReadMap(SagaloreDB.read)
    -- First run: seed every saga already in the log as read so an upgrade is
    -- not a wall of blue. /sagalore wipe|unread (or /sl) clears this for playtesting.
    local entries = currentEntries()
    for i = 1, #entries do
        local id = entries[i].saga_id
        if type(id) == "string" and id ~= "" then
            flat[id] = true
        end
    end
    SagaloreDB.read = flat
    SagaloreDB.read_bootstrapped = true
    return SagaloreDB.read
end

local function markRead(questID)
    if type(questID) ~= "string" or questID == "" then
        return
    end
    local bucket = ensureReadMap()
    bucket[questID] = true
end

-- Mark every incomplete saga unread (and drop completed ids from the map).
local function markAllUnread()
    initDB()
    SagaloreDB.read = {}
    SagaloreDB.read_bootstrapped = true
    local entries = currentEntries()
    for i = 1, #entries do
        local e = entries[i]
        if isComplete(e) and type(e.saga_id) == "string" then
            SagaloreDB.read[e.saga_id] = true
        end
    end
end

local function isUnread(questID, entry)
    if type(questID) ~= "string" or questID == "" then
        return false
    end
    if entry and isComplete(entry) then
        return false
    end
    local bucket = ensureReadMap()
    return bucket[questID] ~= true
end

local function countUnreadSagas()
    local bucket = ensureReadMap()
    local entries = currentEntries()
    local n = 0
    for i = 1, #entries do
        local e = entries[i]
        if not isComplete(e) and type(e.saga_id) == "string" and bucket[e.saga_id] ~= true then
            n = n + 1
        end
    end
    return n
end

local function hasUnreadSagas()
    return countUnreadSagas() > 0
end

local function unreadTooltipLine()
    local n = countUnreadSagas()
    if n <= 0 then
        return nil
    end
    if n == 1 then
        return L("ui.one_new")
    end
    return string.format(L("ui.many_new"), n)
end

-- Medium sky blue from unread.tga (● is missing in the default WoW font).
local UNREAD_BLUE = { r = 0.38, g = 0.68, b = 1.0 }
-- Always leave room for the list dot so rows do not jump when marked read.
local LIST_TEXT_INSET = 20
local function paintUnreadDot(tex)
    if not tex then
        return
    end
    tex:SetTexture("Interface\\AddOns\\Sagalore\\unread")
    tex:SetVertexColor(UNREAD_BLUE.r, UNREAD_BLUE.g, UNREAD_BLUE.b)
    if tex.SetAlpha then
        tex:SetAlpha(1)
    end
end

local function layoutListRow(row, unread)
    row.title:ClearAllPoints()
    row.title:SetPoint("TOPLEFT", LIST_TEXT_INSET, -4)
    row.title:SetPoint("TOPRIGHT", -8, -4)
    row.sub:ClearAllPoints()
    row.sub:SetPoint("BOTTOMLEFT", LIST_TEXT_INSET, 4)
    row.sub:SetPoint("BOTTOMRIGHT", -8, 4)
    if unread then
        row.unread:ClearAllPoints()
        -- Sit beside the title line, not vertically centered on the whole row.
        row.unread:SetPoint("TOPLEFT", 5, -6)
        row.unread:Show()
    else
        row.unread:Hide()
    end
end

updateMinimapUnread = function()
    if not minimapBtn or not minimapBtn.unreadGlow then
        return
    end
    if hasUnreadSagas() then
        minimapBtn.unreadGlow:Show()
    else
        minimapBtn.unreadGlow:Hide()
    end
end

local function formatDescription(text)
    if type(text) ~= "string" or text == "" then
        return L("ui.none")
    end
    text = text:gsub("\r\n", "\n"):gsub("\r", "\n")
    text = text:gsub("\\n", "\n")
    text = text:gsub("[ \t]+\n", "\n"):gsub("\n[ \t]+", "\n")
    text = text:match("^%s*(.-)%s*$") or text
    if text == "" then
        return L("ui.none")
    end
    return text
end

local EN_MONTHS = {
    "January", "February", "March", "April", "May", "June",
    "July", "August", "September", "October", "November", "December",
}

local function isLeapYear(y)
    return (y % 4 == 0 and y % 100 ~= 0) or (y % 400 == 0)
end

local function utcUnix(y, m, d, hh, mm, ss)
    local mdays = { 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31 }
    local days = 0
    for yr = 1970, y - 1 do
        days = days + (isLeapYear(yr) and 366 or 365)
    end
    for mo = 1, m - 1 do
        local dim = mdays[mo]
        if mo == 2 and isLeapYear(y) then
            dim = 29
        end
        days = days + dim
    end
    days = days + d - 1
    return days * 86400 + hh * 3600 + mm * 60 + ss
end

local function lastSundayUTC(year, month)
    local mdays = { 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31 }
    local dim = mdays[month]
    if month == 2 and isLeapYear(year) then
        dim = 29
    end
    for d = dim, dim - 6, -1 do
        local t = date("!*t", utcUnix(year, month, d, 12, 0, 0))
        if t and t.wday == 1 then
            return d
        end
    end
    return dim
end

-- Europe/Oslo: last Sunday of March 01:00 UTC → CEST; last Sunday of October 01:00 UTC → CET.
local function osloOffset(sec)
    local t = date("!*t", sec)
    if not t then
        return 3600
    end
    local start = utcUnix(t.year, 3, lastSundayUTC(t.year, 3), 1, 0, 0)
    local finish = utcUnix(t.year, 10, lastSundayUTC(t.year, 10), 1, 0, 0)
    if sec >= start and sec < finish then
        return 7200
    end
    return 3600
end

local function formatOsloWhen(raw)
    if type(raw) == "string" and raw ~= "" and not tonumber(raw) then
        -- Companion already formatted "dd mmmm yyyy hh:mm".
        if raw:find("%d%d %a+ %d%d%d%d %d%d:%d%d") then
            return raw
        end
    end
    local sec = plainNumber(raw)
    if not sec and type(raw) == "string" then
        local y, mo, d, hh, mm, ss = raw:match("^(%d%d%d%d)%-(%d%d)%-(%d%d)T(%d%d):(%d%d):(%d%d)")
        if y then
            sec = utcUnix(tonumber(y), tonumber(mo), tonumber(d), tonumber(hh), tonumber(mm), tonumber(ss) or 0)
        end
    end
    if not sec or sec < 1 then
        return ""
    end
    sec = math.floor(sec)
    local localSec = sec + osloOffset(sec)
    local t = date("!*t", localSec)
    if not t then
        return ""
    end
    local month = EN_MONTHS[t.month] or ""
    return string.format("%02d %s %d %02d:%02d", t.day, month, t.year, t.hour, t.min)
end

SagaloreBoard = SagaloreBoard or {}
SagaloreBoard.rows = {}

function SagaloreBoard.formatDuration(sec)
    sec = tonumber(sec)
    if not sec or sec < 0 then
        return ""
    end
    sec = math.floor(sec)
    local h = math.floor(sec / 3600)
    local m = math.floor((sec % 3600) / 60)
    local s = sec % 60
    if h > 0 then
        return string.format("%dh %02dm", h, m)
    end
    if m > 0 then
        return string.format("%dm %02ds", m, s)
    end
    return string.format("%ds", s)
end

function SagaloreBoard.sortMode()
    initDB()
    if SagaloreDB.activity_sort == "completion_time" then
        return "completion_time"
    end
    return "completed_at"
end

-- True when the saga has objectives that can meaningfully race on a clock
-- (travel, kills, sequences, etc.). Lone free-standing emotes do not.
local function entryHasTimedSteps(entry)
    if type(entry) ~= "table" or type(entry.objectives) ~= "table" then
        return false
    end
    local n = #entry.objectives
    if n == 0 then
        return false
    end
    for i = 1, n do
        local o = entry.objectives[i]
        if type(o) == "table" then
            local t = o.type
            if t == "sequence" or t == "go" or t == "location" or t == "kill"
                or t == "item" or t == "collect" or t == "instance_run"
                or t == "all_of" or t == "any_of" then
                return true
            end
            if (t == "emote" or t == "talk") and (o.zone or o.subzone or o.map_id or o.x or o.y) then
                return true
            end
        end
    end
    return false
end

local function activityList(entry)
    local out = {}
    local seen = {}
    local function add(row)
        if type(row) ~= "table" then
            return
        end
        local character = plainString(row.character) or ""
        if character == "" then
            return
        end
        local realm = plainString(row.realm) or ""
        local key = (character .. "\n" .. realm):lower()
        if seen[key] then
            return
        end
        seen[key] = true
        local at = ""
        if type(row.completed_at) == "string" then
            at = row.completed_at
        elseif type(row.completed_at) == "number" then
            at = string.format("%014d", row.completed_at)
        end
        local when = plainString(row["when"]) or ""
        if when == "" then
            when = formatOsloWhen(row.completed_at)
        end
        local dur = tonumber(row.duration_sec)
        if dur == nil then
            local started = tonumber(row.started_at)
            local doneAt = tonumber(row.completed_at)
            if started and doneAt and doneAt >= started then
                dur = doneAt - started
            end
        end
        if dur and dur < 0 then
            dur = nil
        end
        table.insert(out, {
            when = when,
            character = character,
            battletag = plainString(row.battletag) or "",
            level = tonumber(row.level) or 0,
            race = plainString(row.race) or "",
            class = plainString(row.class) or "",
            at = at,
            duration_sec = dur,
        })
    end
    if type(entry) == "table" and type(entry.completed_by) == "table" then
        for i = 1, #entry.completed_by do
            add(entry.completed_by[i])
        end
    end
    -- Local finishes before the companion has synced them to the API.
    if type(entry) == "table" and type(entry.saga_id) == "string" then
        initDB()
        local localDone = SagaloreDB.completions and SagaloreDB.completions[entry.saga_id]
        if type(localDone) == "table" then
            add({
                character = localDone.character or player.name,
                realm = localDone.realm or player.realm,
                battletag = player.battletag,
                level = localDone.level or player.level,
                race = localDone.race or player.race,
                class = localDone.class or player.class,
                completed_at = localDone.completed_at,
                started_at = localDone.started_at,
            })
        end
    end
    local mode = "completed_at"
    if entryHasTimedSteps(entry) then
        mode = SagaloreBoard.sortMode()
    end
    table.sort(out, function(a, b)
        if mode == "completion_time" then
            local ad, bd = a.duration_sec, b.duration_sec
            local aNil, bNil = ad == nil, bd == nil
            if aNil ~= bNil then
                return not aNil
            end
            if not aNil and ad ~= bd then
                return ad < bd
            end
        elseif a.at ~= b.at then
            return a.at > b.at
        end
        return a.character < b.character
    end)
    return out
end

local function savedSteps(entry)
    initDB()
    local version = tonumber(entry.saga_version) or 0
    local bucket = SagaloreDB.objectives[entry.saga_id]
    if type(bucket) ~= "table" or tonumber(bucket.version) ~= version then
        bucket = { version = version, steps = {} }
        SagaloreDB.objectives[entry.saga_id] = bucket
    end
    if type(bucket.steps) ~= "table" then
        bucket.steps = {}
    end
    return bucket
end

local function itemQualityHex(quality)
    local row = ITEM_QUALITY_COLORS and ITEM_QUALITY_COLORS[quality]
    if type(row) == "table" and type(row.hex) == "string" and row.hex ~= "" then
        return row.hex
    end
    local fallback = {
        [0] = "|cff9d9d9d",
        [1] = "|cffffffff",
        [2] = "|cff1eff00",
        [3] = "|cff0070dd",
        [4] = "|cffa335ee",
        [5] = "|cffff8000",
        [6] = "|cffe6cc80",
        [7] = "|cff00ccff",
    }
    return fallback[quality] or "|cffffffff"
end

local function itemLink(id, fallbackName)
    local name, link, quality
    if GetItemInfo then
        local ok, a, b, c = pcall(GetItemInfo, id)
        if ok then
            name, link, quality = a, b, c
        end
    end
    if (type(name) ~= "string" or name == "") and C_Item and C_Item.GetItemInfo then
        local ok, a, b, c = pcall(C_Item.GetItemInfo, id)
        if ok then
            name, link, quality = a, b, c
        end
    end
    if type(name) ~= "string" or name == "" then
        if C_Item and C_Item.RequestLoadItemDataByID then
            pcall(C_Item.RequestLoadItemDataByID, id)
        end
        if type(fallbackName) == "string" and fallbackName ~= "" then
            return "|cffffffff[" .. fallbackName .. "]|r"
        end
        return ""
    end
    if type(link) == "string" and string.find(link, "|H", 1, true) then
        return link
    end
    return itemQualityHex(quality) .. "[" .. name .. "]|r"
end

local function applyItemLink(text, obj)
    local id = tonumber(obj.item_id)
    if not id then
        return text
    end
    local link = itemLink(id, obj.item_name)
    if link == "" then
        return text
    end
    local plain = obj.item_name
    if type(plain) == "string" and plain ~= "" then
        local i, j = string.find(text, plain, 1, true)
        if i then
            return string.sub(text, 1, i - 1) .. link .. string.sub(text, j + 1)
        end
    end
    return text .. " " .. link
end

local function creatorName(entry)
    local by = entry and entry.created_by
    if type(by) ~= "string" or by == "" then
        return L("ui.unknown")
    end
    -- Personal starters are stamped Sagalore#0000 in the API.
    if string.lower(by) == "sagalore#0000" then
        return "Sagalore"
    end
    return by
end

local function completionSet()
    local done = {}
    if type(SagaloreDB) == "table" and type(SagaloreDB.completions) == "table" then
        for id in pairs(SagaloreDB.completions) do
            if type(id) == "string" then
                done[id] = true
            end
        end
    end
    return done
end

local function entryLocked(entry, entries)
    if type(entry) ~= "table" or type(SagaloreChains) ~= "table" or type(SagaloreChains.isLocked) ~= "function" then
        return false
    end
    return SagaloreChains.isLocked(entry, entries, completionSet()) and true or false
end

local function entryIsGroup(entry)
    -- Turn-in roster (group_rule), not party visibility.
    return type(entry) == "table" and type(entry.group) == "table"
end

local function listTitle(entry)
    local title = entry.title or "Saga"
    if entryIsGroup(entry) then
        title = title .. L("ui.group_suffix")
    end
    return title
end

local statusFilter = { open = true, locked = true, done = true }
local creatorHidden = {}
local groupHidden = {}
local filtersLoaded = false

local function entryGroupName(entry)
    if type(entry) ~= "table" then
        return ""
    end
    local name = entry.group_name
    if type(name) == "string" and name ~= "" then
        return name
    end
    return ""
end

local function loadFilters()
    if filtersLoaded then
        return
    end
    filtersLoaded = true
    initDB()
    local saved = SagaloreDB.filters
    if type(saved) ~= "table" then
        return
    end
    if type(saved.status) == "table" then
        if saved.status.open ~= nil then
            statusFilter.open = saved.status.open and true or false
        end
        if saved.status.locked ~= nil then
            statusFilter.locked = saved.status.locked and true or false
        end
        if saved.status.done ~= nil then
            statusFilter.done = saved.status.done and true or false
        end
    end
    if type(saved.hiddenCreators) == "table" then
        for name, hidden in pairs(saved.hiddenCreators) do
            if type(name) == "string" and hidden then
                creatorHidden[name] = true
            end
        end
    end
    if type(saved.hiddenGroups) == "table" then
        for name, hidden in pairs(saved.hiddenGroups) do
            if type(name) == "string" and hidden then
                groupHidden[name] = true
            end
        end
    end
end

local function saveFilters()
    initDB()
    local hidden = {}
    for name, on in pairs(creatorHidden) do
        if on then
            hidden[name] = true
        end
    end
    local hiddenGroups = {}
    for name, on in pairs(groupHidden) do
        if on then
            hiddenGroups[name] = true
        end
    end
    SagaloreDB.filters = {
        status = {
            open = statusFilter.open ~= false,
            locked = statusFilter.locked ~= false,
            done = statusFilter.done ~= false,
        },
        hiddenCreators = hidden,
        hiddenGroups = hiddenGroups,
    }
end

local searchQuery = ""
local searchScores = {}

local function lowerText(s)
    s = tostring(s or "")
    -- UTF-8 Norwegian letters before byte-wise lower.
    s = s:gsub("Æ", "æ"):gsub("Ø", "ø"):gsub("Å", "å")
    return string.lower(s)
end

-- Returns rank (2 = title, 1 = description) or nil when no match.
local function searchRank(entry, needle)
    if needle == "" then
        return 0
    end
    local title = lowerText(entry.title)
    local desc = lowerText(entry.description)
    local titleAt = string.find(title, needle, 1, true)
    if titleAt then
        return 2, titleAt
    end
    local descAt = string.find(desc, needle, 1, true)
    if descAt then
        return 1, descAt
    end
    return nil
end

local function passesFilters(entry)
    -- Status is already the open/done list sections; no separate status filter.
    if creatorHidden[creatorName(entry)] then
        return false
    end
    local group = entryGroupName(entry)
    if group ~= "" and groupHidden[group] then
        return false
    end
    local needle = searchQuery
    if needle ~= "" then
        local rank = searchRank(entry, needle)
        if not rank then
            return false
        end
        searchScores[entry.saga_id or ""] = rank
    end
    return true
end

local function sortBySearch(list)
    if searchQuery == "" or #list < 2 then
        return
    end
    table.sort(list, function(a, b)
        local ra = searchScores[a.saga_id or ""] or 0
        local rb = searchScores[b.saga_id or ""] or 0
        if ra ~= rb then
            return ra > rb
        end
        local ta = lowerText(a.title)
        local tb = lowerText(b.title)
        if ta ~= tb then
            return ta < tb
        end
        return tostring(a.saga_id or "") < tostring(b.saga_id or "")
    end)
end

local statusDrop
local groupDrop
local creatorDrop
local searchBox
local scrollSelectIntoView = false

local function updateSearchHint()
    if not searchBox or not searchBox.hint then
        return
    end
    local empty = (searchBox:GetText() or "") == ""
    local focused = searchBox.HasFocus and searchBox:HasFocus()
    if empty and not focused then
        searchBox.hint:Show()
    else
        searchBox.hint:Hide()
    end
end

local function closeMenusIfOutside()
    local list = _G.DropDownList1
    if not list or not list.IsShown or not list:IsShown() then
        return
    end
    if list.IsMouseOver and list:IsMouseOver() then
        return
    end
    local owner = list.dropdown or UIDROPDOWNMENU_OPEN_MENU
    if owner and owner.IsMouseOver and owner:IsMouseOver() then
        return
    end
    if CloseDropDownMenus then
        CloseDropDownMenus()
    else
        list:Hide()
    end
end

local function blurSearch()
    if searchBox then
        searchBox:ClearFocus()
        updateSearchHint()
    end
    closeMenusIfOutside()
end

local MENU_ROW = 22
local MENU_PAD_X = 22
local MENU_PAD_Y = 14

local function spaceDropList(level)
    level = level or 1
    local list = _G["DropDownList" .. level]
    if not list or not list.numButtons or list.numButtons < 1 then
        return
    end
    local n = list.numButtons
    local widest = 48
    for i = 1, n do
        local btn = _G["DropDownList" .. level .. "Button" .. i]
        local fs = btn and _G[btn:GetName() .. "NormalText"]
        local tw = fs and fs.GetStringWidth and fs:GetStringWidth() or 0
        if tw > widest then
            widest = tw
        end
    end
    -- Checkbox sits on the button's left edge, so the inset is the gap
    -- between the menu border and the check / label.
    local width = widest + (MENU_PAD_X * 2) + 36
    if width < 160 then
        width = 160
    end
    list:SetWidth(width)
    -- Buttons span the menu so the yellow hover fills the dropdown.
    -- Checkbox / label are inset for padding; the highlight is not.
    local edge = 2
    local inset = MENU_PAD_X - edge
    if inset < 8 then
        inset = 8
    end
    for i = 1, n do
        local btn = _G["DropDownList" .. level .. "Button" .. i]
        if btn then
            local name = btn:GetName()
            btn:SetHeight(MENU_ROW)
            btn:ClearAllPoints()
            btn:SetPoint("TOPLEFT", list, "TOPLEFT", edge, -(MENU_PAD_Y + (i - 1) * MENU_ROW))
            btn:SetPoint("RIGHT", list, "RIGHT", -edge, 0)
            local check = _G[name .. "Check"]
            local uncheck = _G[name .. "UnCheck"]
            local text = _G[name .. "NormalText"]
            if check then
                check:ClearAllPoints()
                check:SetPoint("LEFT", btn, "LEFT", inset, 0)
            end
            if uncheck then
                uncheck:ClearAllPoints()
                uncheck:SetPoint("LEFT", btn, "LEFT", inset, 0)
            end
            if text then
                text:ClearAllPoints()
                text:SetPoint("LEFT", btn, "LEFT", inset + 18, 0)
            end
            local hl = _G[name .. "Highlight"]
            if hl then
                hl:ClearAllPoints()
                hl:SetPoint("LEFT", list, "LEFT", edge, 0)
                hl:SetPoint("RIGHT", list, "RIGHT", -edge, 0)
                hl:SetPoint("TOP", btn, "TOP", 0, 0)
                hl:SetPoint("BOTTOM", btn, "BOTTOM", 0, 0)
            end
        end
    end
    list:SetHeight((n * MENU_ROW) + (MENU_PAD_Y * 2))
end

local dropListHooked = false
local function hookDropListSpacing()
    if WorldFrame and WorldFrame.HookScript and not SagaloreBoard.worldClickHooked then
        SagaloreBoard.worldClickHooked = true
        WorldFrame:HookScript("OnMouseDown", function()
            closeMenusIfOutside()
        end)
    end
    if dropListHooked then
        return
    end
    local list = _G.DropDownList1
    if not list or not list.HookScript then
        return
    end
    dropListHooked = true
    list:HookScript("OnShow", function(self)
        local owner = self.dropdown or UIDROPDOWNMENU_OPEN_MENU
        if owner == statusDrop or owner == groupDrop or owner == creatorDrop
            or owner == SagaloreBoard.boardGroupDrop or owner == SagaloreBoard.boardPeriodDrop then
            spaceDropList(1)
            -- Stay above the log panels, and don't start the auto-hide countdown
            -- while the cursor is still on the menu. A click outside still closes it.
            self:SetFrameStrata("TOOLTIP")
            self.showTimer = nil
            self.isCounting = nil
        end
    end)
    list:HookScript("OnUpdate", function(self)
        if not self:IsShown() then
            return
        end
        local owner = self.dropdown or UIDROPDOWNMENU_OPEN_MENU
        if owner ~= statusDrop and owner ~= groupDrop and owner ~= creatorDrop
            and owner ~= SagaloreBoard.boardGroupDrop and owner ~= SagaloreBoard.boardPeriodDrop then
            return
        end
        if self:IsMouseOver() or (owner and owner.IsMouseOver and owner:IsMouseOver()) then
            self.showTimer = nil
            self.isCounting = nil
        end
    end)
end

local function filterCheck(text, checked, onClick)
    local info = UIDropDownMenu_CreateInfo()
    info.text = text
    info.isNotRadio = true
    info.keepShownOnClick = true
    info.checked = checked
    info.func = function(button, _, _, value)
        -- Paint on the next frame. Rebuilding the list inside the click
        -- leaves the open menu showing the previous rows until it closes.
        local on
        if type(value) == "boolean" then
            on = value
        elseif button and button.GetName then
            local mark = _G[button:GetName() .. "Check"]
            if mark and mark.IsShown then
                on = mark:IsShown() and true or false
            end
        end
        onClick(on and true or false)
        SagaloreBoard.pendingRefresh = true
    end
    UIDropDownMenu_AddButton(info)
end

local function setupListFilters()
    if not UIDropDownMenu_Initialize then
        return
    end
    if groupDrop then
        UIDropDownMenu_Initialize(groupDrop, function()
            local seen, names = {}, {}
            local entries = currentEntries()
            for i = 1, #entries do
                local name = entryGroupName(entries[i])
                if name ~= "" and not seen[name] then
                    seen[name] = true
                    names[#names + 1] = name
                end
            end
            table.sort(names)
            if #names == 0 then
                filterCheck(L("ui.none"), false, function() end)
                spaceDropList(1)
                return
            end
            for i = 1, #names do
                local name = names[i]
                filterCheck(name, not groupHidden[name], function(on)
                    if on then
                        groupHidden[name] = nil
                    else
                        groupHidden[name] = true
                    end
                    saveFilters()
                end)
            end
            spaceDropList(1)
        end)
        UIDropDownMenu_SetText(groupDrop, L("ui.group"))
    end
    if creatorDrop then
        UIDropDownMenu_Initialize(creatorDrop, function()
            local seen, names = {}, {}
            local entries = currentEntries()
            for i = 1, #entries do
                local name = creatorName(entries[i])
                if not seen[name] then
                    seen[name] = true
                    names[#names + 1] = name
                end
            end
            table.sort(names)
            if #names == 0 then
                filterCheck(L("ui.none"), false, function() end)
                spaceDropList(1)
                return
            end
            for i = 1, #names do
                local name = names[i]
                filterCheck(name, not creatorHidden[name], function(on)
                    if on then
                        creatorHidden[name] = nil
                    else
                        creatorHidden[name] = true
                    end
                    saveFilters()
                end)
            end
            spaceDropList(1)
        end)
        UIDropDownMenu_SetText(creatorDrop, L("ui.created_by"))
    end
    hookDropListSpacing()
end

local function listSubtitle(entry, entries)
    local fallback = creatorName(entry)
    local group = entryGroupName(entry)
    if group ~= "" then
        fallback = string.format(L("ui.group_by"), group, fallback)
    end
    if type(SagaloreChains) ~= "table" or type(SagaloreChains.listSub) ~= "function" then
        return fallback
    end
    return SagaloreChains.listSub(entry, entries, completionSet(), fallback)
end

local function sectionExpanded(key, defaultOpen)
    initDB()
    if type(SagaloreDB.sections) ~= "table" then
        SagaloreDB.sections = {}
    end
    local v = SagaloreDB.sections[key]
    if v == nil then
        return defaultOpen
    end
    return v and true or false
end

local function setSectionExpanded(key, open)
    initDB()
    if type(SagaloreDB.sections) ~= "table" then
        SagaloreDB.sections = {}
    end
    SagaloreDB.sections[key] = open and true or false
end

readClocks = function()
    local clocks = {}
    if GetGameTime then
        local ok, h, m = pcall(GetGameTime)
        h = ok and plainNumber(h) or nil
        m = ok and plainNumber(m) or nil
        if h and m then
            world.gameHour, world.gameMin = h, m
        end
    end
    if world.gameHour and world.gameMin then
        clocks.game = { hour = world.gameHour, min = world.gameMin }
    end
    if date and time then
        local ok, t = pcall(date, "*t")
        local unix = plainNumber(time())
        if ok and type(t) == "table" then
            local h, m = plainNumber(t.hour), plainNumber(t.min)
            if h and m then
                clocks["local"] = { hour = h, min = m, unix = unix }
            end
        end
    end
    if GetServerTime and date then
        local ok, server = pcall(GetServerTime)
        server = ok and plainNumber(server) or nil
        if server and server > 0 then
            local okT, t = pcall(date, "!*t", server)
            if okT and type(t) == "table" then
                local h, m = plainNumber(t.hour), plainNumber(t.min)
                if h and m then
                    clocks.realm = { hour = h, min = m, unix = server }
                end
            end
        end
    end
    return clocks
end

local function readInstance()
    if not GetInstanceInfo then
        return world.instanceName or "", world.instanceType or "none"
    end
    local ok, name, instanceType = pcall(GetInstanceInfo)
    if not ok or secret(name) or secret(instanceType) then
        return world.instanceName or "", world.instanceType or "none"
    end
    local typ = plainString(instanceType) or "none"
    local label = plainString(name)
    if typ == "none" or not label then
        world.instanceName = ""
        world.instanceType = "none"
        return "", "none"
    end
    world.instanceName = label
    world.instanceType = typ
    return label, typ
end

readGroup = function()
    local members, seen, blocked = {}, {}, false
    local function add(unit, fallbackName, fallbackRealm, fallbackLevel)
        local name, realm, level
        if UnitName then
            local ok, n, r = pcall(UnitName, unit)
            if ok and secret(n) then
                blocked = true
                return
            end
            name = ok and plainString(n) or nil
            realm = ok and plainString(r) or nil
        end
        if (not name or name == "") and fallbackName and fallbackName ~= "" then
            name = fallbackName
            realm = fallbackRealm
        end
        if not name or name == "" then
            return
        end
        if UnitLevel then
            local okL, lvl = pcall(UnitLevel, unit)
            if okL and secret(lvl) then
                blocked = true
            else
                level = okL and plainNumber(lvl) or nil
            end
        end
        if (not level or level < 1) and fallbackLevel and fallbackLevel > 0 then
            level = fallbackLevel
        end
        local id = norm(name)
        if seen[id] then
            return
        end
        seen[id] = true
        if not realm or realm == "" then
            realm = player.realm or ""
        end
        table.insert(members, { name = name, realm = realm, level = level or 0 })
    end
    add("player", player.name, player.realm, player.level)
    local grouped = false
    if IsInGroup then
        local ok, v = pcall(IsInGroup)
        grouped = ok and v == true and not secret(v)
    end
    if grouped then
        local raid = false
        if IsInRaid then
            local ok, v = pcall(IsInRaid)
            raid = ok and v == true and not secret(v)
        end
        local count, includesPlayer = 0, true
        if GetNumGroupMembers then
            local ok, v = pcall(GetNumGroupMembers)
            local n = ok and plainNumber(v) or nil
            if n and n > 0 then
                count = n
            end
        end
        if count == 0 and raid and GetNumRaidMembers then
            local ok, v = pcall(GetNumRaidMembers)
            local n = ok and plainNumber(v) or nil
            if n then
                count = n
            end
        end
        if count == 0 and GetNumPartyMembers then
            local ok, v = pcall(GetNumPartyMembers)
            local n = ok and plainNumber(v) or nil
            if n then
                count, includesPlayer = n, false
            end
        end
        local prefix = raid and "raid" or "party"
        local slots = count
        if not raid and includesPlayer then
            slots = count - 1
        end
        if slots < 0 then
            slots = 0
        elseif slots > 40 then
            slots = 40
        end
        for i = 1, slots do
            add(prefix .. i)
            if blocked then
                break
            end
        end
    end
    if not blocked then
        world.groupSnap = members
    end
    local snap = world.groupSnap
    if type(snap) ~= "table" or #snap == 0 then
        snap = { { name = player.name, realm = player.realm or "", level = player.level or 1 } }
    end
    return { instance = readInstance(), zone = world.zone, members = snap }
end

local function clockState()
    return { time = readClocks() }
end

local function timeHint(when, done, clocks)
    if type(when) ~= "table" then
        return ""
    end
    if SagaloreObjectives and type(SagaloreObjectives.timeHint) == "function" then
        return SagaloreObjectives.timeHint(when, clocks, done) or ""
    end
    return ""
end

local function objectiveLines(entry)
    local objs = entry.objectives
    local lines = {}
    if type(objs) ~= "table" then
        local text = type(objs) == "string" and objs or ""
        if text == "" then
            return lines
        end
        for line in string.gmatch(text, "[^\n]+") do
            table.insert(lines, { done = false, text = line })
        end
        return lines
    end
    local bucket = savedSteps(entry)
    local clocks = clockState()
    local function addObj(obj, index, depth)
        if type(obj) ~= "table" then
            return
        end
        local id = obj.id or tostring(index)
        local step = bucket.steps[id] or {}
        local done = step.done and true or false
        local pad = ""
        if depth > 0 then
            pad = string.rep("  ", depth) .. "- "
        end
        local mem = obj.memory
        if mem ~= "visit" and mem ~= "hold" then
            if obj.type == "item" or obj.type == "collect" then
                mem = "hold"
            else
                mem = "visit"
            end
        end
        local label
        local goalText = obj.text or L("ui.goal")
        if done and mem == "hold" then
            label = pad .. string.format(L("ui.goal_now"), goalText)
        elseif done then
            label = pad .. string.format(L("ui.goal_done"), goalText)
        else
            label = pad .. applyItemLink(goalText, obj)
            local groupedKill = obj.type == "kill" and type(obj.group) == "table" and obj.group.level
            if obj.type == "collect" or obj.type == "item" or (obj.type == "kill" and not groupedKill) or obj.type == "instance_run" then
                local need = tonumber(obj.count) or 1
                local have = tonumber(step.count) or 0
                if obj.type == "collect" or obj.type == "item" then
                    local itemID = tonumber(obj.item_id)
                    if itemID then
                        local live = itemCount(itemID)
                        if live > have then
                            have = live
                        end
                    end
                end
                label = string.format("%s (%d/%d)", label, have, need)
            end
            if obj.type == "level" then
                local need = tonumber(obj.level) or 0
                local have = tonumber(step.count) or tonumber(player.level) or 0
                if need > 0 then
                    label = string.format("%s (%d/%d)", label, have, need)
                end
            end
            if obj.type == "all_of" or obj.type == "any_of" then
                local need = tonumber(step.need) or (type(obj.objectives) == "table" and #obj.objectives or 0)
                local have = tonumber(step.count) or 0
                label = string.format("%s (%d/%d)", label, have, need)
            end
            if obj.type == "sequence" and step.failed then
                label = label .. L("ui.failed")
            end
            if obj.type == "kill" and type(obj.group) == "table" and obj.group.level then
                local roster = readGroup and readGroup() or {}
                local have, total = 0, 0
                local level = tonumber(obj.group.level) or 0
                for _, m in ipairs(roster.members or {}) do
                    if type(m) == "table" and type(m.name) == "string" and m.name ~= "" then
                        total = total + 1
                        if (tonumber(m.level) or 0) == level then
                            have = have + 1
                        end
                    end
                end
                local need = tonumber(obj.group.size) or total
                label = label .. string.format(L("ui.kill_group_level"), have, need, level)
            end
            label = label .. timeHint(obj.when, done, clocks)
        end
        table.insert(lines, { done = done, failed = (not done) and step.failed and true or false, text = label })
        if obj.type == "sequence" and type(obj.beats) == "table" then
            local beat = tonumber(step.beat) or 1
            local failed = (not done) and step.failed and true or false
            local remain = tonumber(step.remain) or 0
            for i, b in ipairs(obj.beats) do
                if type(b) == "table" then
                    local beatDone = (not failed) and (done or i < beat)
                    local current = (not failed) and (not done) and i == beat
                    local beatLabel = b.text or L("ui.part")
                    if current and remain > 0 then
                        beatLabel = beatLabel .. string.format(" (%ds)", remain)
                    end
                    table.insert(lines, {
                        done = beatDone,
                        failed = failed,
                        indent = 1,
                        text = beatLabel,
                    })
                end
            end
        elseif type(obj.objectives) == "table" then
            for i, child in ipairs(obj.objectives) do
                addObj(child, i, depth + 1)
            end
        end
    end
    for i, obj in ipairs(objs) do
        addObj(obj, i, 0)
    end
    if type(entry.when) == "table" and SagaloreObjectives and SagaloreObjectives.timeOpen then
        local open = SagaloreObjectives.timeOpen(entry.when, clocks)
        table.insert(lines, { done = open, text = L("ui.time") .. timeHint(entry.when, open, clocks) })
    end
    if type(entry.group) == "table" and tonumber(entry.group.size) and readGroup and SagaloreObjectives and SagaloreObjectives.groupOK then
        local group = readGroup()
        local minLevel = tonumber(entry.group.min_level) or 1
        local have = 0
        for _, m in ipairs(group.members or {}) do
            if type(m) == "table" and (tonumber(m.level) or 0) >= minLevel and type(m.name) == "string" and m.name ~= "" then
                have = have + 1
            end
        end
        local label = string.format(L("ui.with_group"), have, tonumber(entry.group.size))
        if entry.group.min_level then
            label = label .. string.format(L("ui.min_level"), minLevel)
        end
        if type(entry.group.instance) == "string" and entry.group.instance ~= "" then
            label = label .. string.format(L("ui.in_instance"), entry.group.instance)
        end
        table.insert(lines, { done = SagaloreObjectives.groupOK(entry.group, group), text = label })
    end
    return lines
end

local function inInstance()
    if not IsInInstance then
        return false
    end
    local ok, inside = pcall(IsInInstance)
    if not ok or secret(inside) then
        return true
    end
    return inside and true or false
end

local function broadcast(message)
    if inInstance() then
        return
    end
    if not ((C_ChatInfo and C_ChatInfo.SendAddonMessage) or SendAddonMessage) then
        return
    end
    local inRaid, inGroup, inGuild = false, false, false
    pcall(function()
        inRaid = IsInRaid() and true or false
    end)
    pcall(function()
        inGroup = IsInGroup() and true or false
    end)
    pcall(function()
        inGuild = IsInGuild() and true or false
    end)
    if inRaid then
        sendAddon(PREFIX, message, "RAID")
    elseif inGroup then
        sendAddon(PREFIX, message, "PARTY")
    end
    if inGuild then
        sendAddon(PREFIX, message, "GUILD")
    end
end

local function bagItemCount(id)
    local slotsOf = (C_Container and C_Container.GetContainerNumSlots) or GetContainerNumSlots
    local idOf = (C_Container and C_Container.GetContainerItemID) or GetContainerItemID
    local infoOf = (C_Container and C_Container.GetContainerItemInfo) or GetContainerItemInfo
    if type(slotsOf) ~= "function" then
        return nil
    end
    local total, found = 0, false
    for bag = 0, 4 do
        local okSlots, slots = pcall(slotsOf, bag)
        local nSlots = okSlots and plainNumber(slots) or 0
        for slot = 1, nSlots or 0 do
            local itemID, count
            if type(idOf) == "function" then
                local okID, got = pcall(idOf, bag, slot)
                itemID = okID and plainNumber(got) or nil
            end
            if type(infoOf) == "function" then
                local okInfo, a, b = pcall(infoOf, bag, slot)
                if okInfo and type(a) == "table" then
                    itemID = itemID or plainNumber(a.itemID)
                    count = plainNumber(a.stackCount) or 1
                elseif okInfo then
                    count = plainNumber(b) or 1
                end
            end
            if itemID == id then
                found = true
                total = total + (count or 1)
            end
        end
    end
    if not found then
        return nil
    end
    return total
end

itemCount = function(id)
    local best = 0
    local function consider(n)
        local v = plainNumber(n)
        if v and v > best then
            best = v
        end
    end
    if C_Item and C_Item.GetItemCount then
        local ok, n = pcall(C_Item.GetItemCount, id)
        if ok then
            consider(n)
        end
    end
    if GetItemCount then
        local ok, n = pcall(GetItemCount, id)
        if ok then
            consider(n)
        end
    end
    consider(bagItemCount(id))
    return best
end

local function readLocation()
    if InCombatLockdown and InCombatLockdown() then
        return
    end
    if GetZoneText then
        local ok, v = pcall(GetZoneText)
        local s = ok and plainString(v) or nil
        if s then
            world.zone = s
        end
    end
    if GetSubZoneText then
        local ok, v = pcall(GetSubZoneText)
        local s = ok and plainString(v) or nil
        if s then
            world.subzone = s
        end
    end
    if GetMinimapZoneText then
        local ok, v = pcall(GetMinimapZoneText)
        local s = ok and plainString(v) or nil
        if s then
            world.minimap = s
        end
    end
    local id
    if C_Map and C_Map.GetBestMapForUnit then
        local ok, mapID = pcall(C_Map.GetBestMapForUnit, "player")
        id = ok and plainNumber(mapID) or nil
    end
    if id then
        world.mapID = id
    end
    local function asNumber(v)
        if type(v) == "number" then
            return v
        end
        if type(v) == "string" then
            return tonumber(v)
        end
        return nil
    end
    -- Classic returns the position as a mixin object. That object is not
    -- always a plain table, so .x/.y and GetXY have to be read off it directly.
    local function xyOf(v, extra)
        local a, b = asNumber(v), asNumber(extra)
        if a and b then
            return a, b
        end
        if v == nil then
            return nil, nil
        end
        if type(v.GetXY) == "function" then
            local ok, px, py = pcall(v.GetXY, v)
            if ok then
                px, py = asNumber(px), asNumber(py)
                if px and py then
                    return px, py
                end
            end
        end
        local px, py = asNumber(v.x), asNumber(v.y)
        if px and py then
            return px, py
        end
        return nil, nil
    end
    local x, y
    if id and C_Map and C_Map.GetPlayerMapPosition then
        local posOK, a, b = pcall(C_Map.GetPlayerMapPosition, id, "player")
        if posOK then
            x, y = xyOf(a, b)
        end
    end
    if (not x or not y) and GetPlayerMapPosition then
        if SetMapToCurrentZone then
            pcall(SetMapToCurrentZone)
        end
        local posOK, a, b = pcall(GetPlayerMapPosition, "player")
        if posOK and type(a) == "number" and type(b) == "number" then
            x, y = a, b
        end
    end
    x = plainNumber(x)
    y = plainNumber(y)
    if x and y then
        world.x = x
        world.y = y
    end
end

-- Keep the name briefly so a visit talk can latch even if the panel closes
-- before the next full check. Gossip-less interacts use the same latch.
local function noteTalkName(name)
    local s = plainString(name)
    if not s then
        return
    end
    world.pendingTalk = { name = s, untilAt = unixNow() + 5 }
end

local function readNPC()
    if not UnitName then
        return
    end
    local ok, name = pcall(UnitName, "npc")
    local s = ok and plainString(name) or nil
    if s then
        world.npc = s
        noteTalkName(s)
    end
end

local function clearNPC()
    world.npc = ""
end

local function readObject()
    if not UnitName then
        return
    end
    if UnitIsPlayer then
        local ok, isPlayer = pcall(UnitIsPlayer, "target")
        if ok and isPlayer == true and not secret(isPlayer) then
            return
        end
    end
    local ok, name = pcall(UnitName, "target")
    local s = ok and plainString(name) or nil
    if s then
        world.object = s
    end
end

local refresh
local finishQuest
local lastCheckError = ""

local function collectItems(obj, items)
    if type(obj) ~= "table" then
        return
    end
    if obj.type == "collect" or obj.type == "item" then
        local id = tonumber(obj.item_id)
        if id and items[id] == nil then
            items[id] = itemCount(id)
        end
    end
    if type(obj.objectives) == "table" then
        for _, child in ipairs(obj.objectives) do
            collectItems(child, items)
        end
    end
    if type(obj.beats) == "table" then
        for _, child in ipairs(obj.beats) do
            collectItems(child, items)
        end
    end
end

local function objectiveState(entries)
    local items = {}
    for _, entry in ipairs(entries) do
        if type(entry.objectives) == "table" and not isComplete(entry) then
            for _, obj in ipairs(entry.objectives) do
                collectItems(obj, items)
            end
        end
    end
    local emote, emoteTarget, emoteText, emoteZone, emoteSub, emoteMini, emoteMap, emoteX, emoteY
    local pending = world.pendingEmote
    if type(pending) == "table" and (tonumber(pending.untilAt) or 0) >= unixNow() then
        emote = pending.token
        emoteTarget = pending.target
        emoteText = pending.text
        emoteZone = pending.zone
        emoteSub = pending.subzone
        emoteMini = pending.minimap
        emoteMap = pending.mapID
        emoteX = pending.x
        emoteY = pending.y
    end
    local talkName = world.npc
    local pendingTalk = world.pendingTalk
    if (not talkName or talkName == "") and type(pendingTalk) == "table"
        and (tonumber(pendingTalk.untilAt) or 0) >= unixNow() then
        talkName = pendingTalk.name
    end
    return {
        zone = world.zone,
        subzone = world.subzone,
        minimap = world.minimap,
        map_id = world.mapID,
        x = world.x,
        y = world.y,
        npc = world.npc,
        talk_name = talkName,
        object = world.object,
        items = items,
        kill_name = world.killName,
        kill_secret = world.killSecret,
        level = player.level,
        instance_name = world.instanceName or "",
        instance_type = world.instanceType or "none",
        emote = emote,
        emote_target = emoteTarget,
        emote_text = emoteText,
        emote_zone = emoteZone,
        emote_subzone = emoteSub,
        emote_minimap = emoteMini,
        emote_map_id = emoteMap,
        emote_x = emoteX,
        emote_y = emoteY,
        time = readClocks(),
    }
end

local function checkAll()
    if checking or type(SagaloreObjectives) ~= "table" or type(SagaloreObjectives.evaluate) ~= "function" then
        return
    end
    checking = true
    local ok, err = pcall(function()
        local entries = currentEntries()
        local state = objectiveState(entries)
        local group = readGroup()
        readInstance()
        state.now = unixNow()
        state.group = group
        state.death_name = world.deathName
        state.level = player.level
        state.instance_name = world.instanceName or ""
        state.instance_type = world.instanceType or "none"
        for _, entry in ipairs(entries) do
            if not isComplete(entry) and not entryLocked(entry, entries) and type(entry.objectives) == "table" and #entry.objectives > 0 then
                local bucket = savedSteps(entry)
                local ordered = entry.objectives_ordered and true or false
                state.attests = world.attests[entry.saga_id]
                local steps, done = SagaloreObjectives.evaluate(entry.objectives, state, bucket.steps, ordered, entry.when)
                if state.emote and state.emote ~= "" then
                    for _, row in pairs(steps) do
                        if type(row) == "table" and row.consumed_emote then
                            world.pendingEmote = nil
                            break
                        end
                    end
                end
                if SagaloreObjectives.noteSagaStart then
                    SagaloreObjectives.noteSagaStart(bucket, steps, state.now)
                end
                bucket.steps = steps
                local groupReady = true
                if type(entry.group) == "table" and SagaloreObjectives.groupOK then
                    groupReady = SagaloreObjectives.groupOK(entry.group, group)
                end
                if done and groupReady then
                    finishQuest(entry, group)
                end
            end
        end
        world.killName = nil
        world.killSecret = false
        world.deathName = nil
    end)
    checking = false
    if not ok and err ~= lastCheckError then
        lastCheckError = err
        chat("[Sagalore] " .. string.format(L("ui.error"), tostring(err)))
    elseif ok then
        lastCheckError = ""
    end
end

local function onGoalLinkClick(_, link, text, button)
    if IsModifiedClick and IsModifiedClick("DRESSUP") and DressUpItemLink then
        pcall(DressUpItemLink, link)
        return
    end
    if SetItemRef then
        pcall(SetItemRef, link, text, button or "LeftButton")
    end
end

local function onGoalLinkEnter(owner, link)
    if not GameTooltip or type(link) ~= "string" then
        return
    end
    GameTooltip:SetOwner(owner, "ANCHOR_CURSOR")
    local ok = pcall(GameTooltip.SetHyperlink, GameTooltip, link)
    if ok then
        GameTooltip:Show()
    end
end

local function onGoalLinkLeave()
    if GameTooltip then
        GameTooltip:Hide()
    end
end

local function ensureGoalRow(i)
    local row = goalRows[i]
    if row then
        return row
    end
    row = CreateFrame("Frame", nil, detailChild)
    row:SetHeight(18)
    local box = row:CreateTexture(nil, "ARTWORK")
    box:SetSize(14, 14)
    box:SetPoint("TOPLEFT", 0, 1)
    row.box = box
    local text = row:CreateFontString(nil, "OVERLAY", "GameFontHighlight")
    text:SetPoint("TOPLEFT", box, "TOPRIGHT", 8, 0)
    text:SetPoint("TOPRIGHT", row, "TOPRIGHT", 0, 0)
    text:SetJustifyH("LEFT")
    text:SetJustifyV("TOP")
    text:SetWordWrap(true)
    text:SetSpacing(2)
    text:SetTextColor(INK.r, INK.g, INK.b)
    if text.SetHyperlinksEnabled then
        text:SetHyperlinksEnabled(true)
    end
    pcall(function()
        text:SetScript("OnHyperlinkClick", onGoalLinkClick)
        text:SetScript("OnHyperlinkEnter", onGoalLinkEnter)
        text:SetScript("OnHyperlinkLeave", onGoalLinkLeave)
    end)
    row:EnableMouse(true)
    bindWheel(row, detailScroll, 28)
    bindWheel(text, detailScroll, 28)
    row.text = text
    goalRows[i] = row
    return row
end

local function activityTooltipBody(level, race, className)
    local line = ""
    local n = tonumber(level) or 0
    if n > 0 then
        line = "Level " .. tostring(n)
    end
    race = plainString(race) or ""
    className = plainString(className) or ""
    if race ~= "" then
        if line ~= "" then
            line = line .. " "
        end
        line = line .. race
    end
    if className ~= "" then
        if line ~= "" then
            line = line .. " "
        end
        line = line .. className
    end
    return line
end

local function ensureActivityRow(i)
    local row = activityRows[i]
    if row then
        return row
    end
    row = CreateFrame("Frame", nil, detailChild)
    row:SetHeight(36)
    row:EnableMouse(true)
    local bullet = row:CreateFontString(nil, "OVERLAY", "GameFontHighlight")
    bullet:SetPoint("TOPLEFT", 0, 0)
    bullet:SetText("•")
    bullet:SetTextColor(INK.r, INK.g, INK.b)
    row.bullet = bullet
    local whenFS = row:CreateFontString(nil, "OVERLAY", "GameFontHighlightSmall")
    whenFS:SetPoint("TOPLEFT", bullet, "TOPRIGHT", 6, 0)
    whenFS:SetJustifyH("LEFT")
    whenFS:SetJustifyV("TOP")
    whenFS:SetTextColor(INK_MUTED.r, INK_MUTED.g, INK_MUTED.b)
    local charFS = row:CreateFontString(nil, "OVERLAY", "GameFontHighlight")
    charFS:SetPoint("TOPLEFT", whenFS, "BOTTOMLEFT", 0, -2)
    charFS:SetJustifyH("LEFT")
    charFS:SetJustifyV("TOP")
    charFS:SetTextColor(INK.r, INK.g, INK.b)
    row.whenFS = whenFS
    row.charFS = charFS
    row:SetScript("OnEnter", function(self)
        if not GameTooltip then
            return
        end
        local name = self.tipName or ""
        if name == "" then
            return
        end
        GameTooltip:SetOwner(self, "ANCHOR_CURSOR")
        GameTooltip:ClearLines()
        GameTooltip:AddLine(name, 1, 1, 1)
        local tip = activityTooltipBody(self.tipLevel, self.tipRace, self.tipClass)
        if tip ~= "" then
            GameTooltip:AddLine(tip, 1, 1, 1)
        end
        local tag = plainString(self.tipTag)
        if tag then
            GameTooltip:AddLine(tag, 0.7, 0.7, 0.7)
        end
        GameTooltip:Show()
    end)
    row:SetScript("OnLeave", function()
        if GameTooltip then
            GameTooltip:Hide()
        end
    end)
    bindWheel(row, detailScroll, 28)
    activityRows[i] = row
    return row
end

local function ensureRow(i)
    local row = rows[i]
    if row then
        return row
    end
    row = CreateFrame("Button", nil, listChild)
    row:SetSize(210, 38)
    local bg = row:CreateTexture(nil, "BACKGROUND")
    bg:SetAllPoints()
    bg:SetTexture("Interface\\QuestFrame\\UI-QuestTitleHighlight")
    bg:SetBlendMode("ADD")
    bg:SetAlpha(0)
    row.bg = bg
    local hover = row:CreateTexture(nil, "BACKGROUND", nil, 1)
    hover:SetAllPoints()
    hover:SetTexture("Interface\\QuestFrame\\UI-QuestTitleHighlight")
    hover:SetBlendMode("ADD")
    hover:SetVertexColor(0.55, 0.42, 0.22)
    hover:SetAlpha(0)
    row.hover = hover
    local unread = row:CreateTexture(nil, "OVERLAY")
    unread:SetSize(9, 9)
    unread:SetPoint("TOPLEFT", 5, -6)
    paintUnreadDot(unread)
    unread:Hide()
    row.unread = unread
    local title = row:CreateFontString(nil, "OVERLAY", "GameFontNormal")
    title:SetPoint("TOPLEFT", LIST_TEXT_INSET, -4)
    title:SetPoint("TOPRIGHT", -8, -4)
    title:SetJustifyH("LEFT")
    title:SetWordWrap(false)
    row.title = title
    local sub = row:CreateFontString(nil, "OVERLAY", "GameFontHighlightSmall")
    sub:SetPoint("BOTTOMLEFT", LIST_TEXT_INSET, 4)
    sub:SetPoint("BOTTOMRIGHT", -8, 4)
    sub:SetJustifyH("LEFT")
    row.sub = sub
    bindWheel(row, listScroll, 36)
    bindWheel(title, listScroll, 36)
    bindWheel(sub, listScroll, 36)
    row:SetScript("OnClick", function(self)
        blurSearch()
        selectedID = self.questID
        markRead(self.questID)
        refresh()
    end)
    row:SetScript("OnEnter", function(self)
        if self.questID ~= selectedID then
            self.hover:SetAlpha(0.45)
        end
    end)
    row:SetScript("OnLeave", function(self)
        self.hover:SetAlpha(0)
    end)
    rows[i] = row
    return row
end

local function placeSectionHeader(header, y, label, expanded, onToggle)
    header:ClearAllPoints()
    header:SetPoint("TOPLEFT", listChild, "TOPLEFT", 0, -y)
    header:SetPoint("TOPRIGHT", listChild, "TOPRIGHT", 0, -y)
    header:Show()
    if expanded then
        header.icon:SetTexture("Interface\\Buttons\\UI-MinusButton-UP")
    else
        header.icon:SetTexture("Interface\\Buttons\\UI-PlusButton-UP")
    end
    header.label:SetText(label)
    header:SetScript("OnClick", onToggle)
    return y + 24
end

function SagaloreBoard.currentView()
    initDB()
    if SagaloreDB.view == "board" then
        return "board"
    end
    return "log"
end

function SagaloreBoard.currentPeriod()
    initDB()
    local p = SagaloreDB.board_period
    if p == "month" or p == "week" or p == "today" or p == "all" then
        return p
    end
    return "all"
end

function SagaloreBoard.dataGroups()
    local groups = SagaloreData and SagaloreData.groups
    if type(groups) ~= "table" then
        return {}
    end
    local out = {}
    for i = 1, #groups do
        if type(groups[i]) == "table" and type(groups[i].id) == "string" and groups[i].id ~= "" then
            out[#out + 1] = groups[i]
        end
    end
    return out
end

function SagaloreBoard.currentBoardGroup()
    initDB()
    local groups = SagaloreBoard.dataGroups()
    local want = SagaloreDB.board_group
    for i = 1, #groups do
        if groups[i].id == want then
            return groups[i]
        end
    end
    return groups[1]
end

function SagaloreBoard.findLeaderboard(groupID, period)
    local boards = SagaloreData and SagaloreData.leaderboards
    if type(boards) ~= "table" or type(groupID) ~= "string" then
        return nil
    end
    for i = 1, #boards do
        local row = boards[i]
        if type(row) == "table" and row.group_id == groupID and row.period == period then
            return row
        end
    end
    return nil
end

function SagaloreBoard.tintTab(tab)
    local name = tab and tab.GetName and tab:GetName()
    if not name then
        return
    end
    local parts = { "Left", "Middle", "Right", "LeftDisabled", "MiddleDisabled", "RightDisabled" }
    for i = 1, #parts do
        local tex = _G[name .. parts[i]]
        if tex and tex.SetVertexColor then
            tex:SetVertexColor(1, 0.9, 0.58)
        end
    end
end

-- The dialog edge cannot be clipped. A short mask, only as wide as the
-- active tab, covers the gold line and stops there.
function SagaloreBoard.ensureTabMask(tab)
    if tab.masks or not frame then
        return
    end
    tab.masks = {}
    -- Stacked: DialogBox-Background is translucent on its own.
    for i = 1, 3 do
        local t = tab:CreateTexture(nil, "BACKGROUND", nil, i)
        t:SetTexture("Interface\\DialogFrame\\UI-DialogBox-Background")
        if t.SetHorizTile then
            t:SetHorizTile(true)
        end
        if t.SetVertTile then
            t:SetVertTile(true)
        end
        t:SetPoint("BOTTOMLEFT", tab, "TOPLEFT", 16, 0)
        t:SetPoint("BOTTOMRIGHT", tab, "TOPRIGHT", -16, 0)
        t:SetHeight(4)
        t:Hide()
        tab.masks[i] = t
    end
end

function SagaloreBoard.placeTabs(active)
    local tabs = { SagaloreBoard.sagasTab, SagaloreBoard.boardTab }
    if not frame or not tabs[1] or not tabs[2] then
        return
    end
    -- Same baseline: tab tops sit on the dialog's bottom gold edge.
    local y = 6
    local x = 11
    local level = frame:GetFrameLevel() or 1
    for i = 1, 2 do
        local tab = tabs[i]
        SagaloreBoard.tintTab(tab)
        SagaloreBoard.ensureTabMask(tab)
        tab:ClearAllPoints()
        if i == 1 then
            tab:SetPoint("TOPLEFT", frame, "BOTTOMLEFT", x, y)
        else
            local w = tabs[1]:GetWidth() or 0
            if w < 40 then
                w = 80
            end
            tab:SetPoint("TOPLEFT", frame, "BOTTOMLEFT", x + w - 15, y)
        end
        local on = i == active
        tab:SetFrameLevel(level + (on and 8 or 2))
        if tab.masks then
            for m = 1, #tab.masks do
                if on then
                    tab.masks[m]:Show()
                else
                    tab.masks[m]:Hide()
                end
            end
        end
    end
end

function SagaloreBoard.paintViewButtons()
    if not frame then
        return
    end
    local id = SagaloreBoard.currentView() == "board" and 2 or 1
    if frame.selectedTab ~= id and PanelTemplates_SetTab then
        pcall(PanelTemplates_SetTab, frame, id)
    end
    SagaloreBoard.placeTabs(id)
end

function SagaloreBoard.menuOpen()
    local list = _G.DropDownList1
    return list and list.IsShown and list:IsShown()
end

function SagaloreBoard.applyBoardSkin()
    if detailPanel and detailPanel.parchment then
        detailPanel.parchment:Hide()
    end
    if detailPanel and detailPanel.SetBackdropColor then
        detailPanel:SetBackdropColor(0, 0, 0, 0)
    end
    if detailPanel and detailPanel.SetBackdropBorderColor then
        detailPanel:SetBackdropBorderColor(0, 0, 0, 0)
    end
end

function SagaloreBoard.applyLogSkin()
    if detailPanel and detailPanel.parchment then
        detailPanel.parchment:Show()
    end
    if detailPanel and detailPanel.SetBackdropColor then
        detailPanel:SetBackdropColor(0.86, 0.78, 0.58, 0.97)
    end
    if detailPanel and detailPanel.SetBackdropBorderColor then
        detailPanel:SetBackdropBorderColor(GOLD.r, GOLD.g, GOLD.b, 1)
    end
end

function SagaloreBoard.hideBoardChrome()
    for i = 1, #SagaloreBoard.rows do
        SagaloreBoard.rows[i]:Hide()
    end
    if SagaloreBoard.emptyFS then
        SagaloreBoard.emptyFS:Hide()
    end
    if SagaloreBoard.reloadHintFS then
        SagaloreBoard.reloadHintFS:Hide()
    end
    if SagaloreBoard.boardGroupDrop then
        SagaloreBoard.boardGroupDrop:Hide()
    end
    if SagaloreBoard.boardPeriodDrop then
        SagaloreBoard.boardPeriodDrop:Hide()
    end
end

function SagaloreBoard.layoutLogPanels()
    SagaloreBoard.applyLogSkin()
    if SagaloreBoard.layoutMode == "log" then
        if listPanel then
            listPanel:Show()
        end
        return
    end
    SagaloreBoard.layoutMode = "log"
    if listPanel then
        listPanel:ClearAllPoints()
        listPanel:SetPoint("TOPLEFT", 22, -72)
        listPanel:SetPoint("BOTTOMLEFT", 22, 22)
        listPanel:SetSize(236, 410)
        listPanel:Show()
    end
    if detailPanel then
        detailPanel:ClearAllPoints()
        detailPanel:SetPoint("TOPLEFT", listPanel, "TOPRIGHT", 14, 0)
        detailPanel:SetPoint("BOTTOMRIGHT", frame, "BOTTOMRIGHT", -18, 22)
        detailPanel:Show()
    end
end

function SagaloreBoard.layoutBoardPanels()
    SagaloreBoard.applyBoardSkin()
    if SagaloreBoard.layoutMode == "board" then
        if listPanel then
            listPanel:Hide()
        end
        return
    end
    SagaloreBoard.layoutMode = "board"
    if listPanel then
        listPanel:Hide()
    end
    if detailPanel then
        detailPanel:ClearAllPoints()
        detailPanel:SetPoint("TOPLEFT", frame, "TOPLEFT", 22, -72)
        detailPanel:SetPoint("BOTTOMRIGHT", frame, "BOTTOMRIGHT", -18, 22)
        detailPanel:Show()
    end
end

function SagaloreBoard.showLogChrome()
    SagaloreBoard.hideBoardChrome()
    SagaloreBoard.layoutLogPanels()
    if searchBox then
        searchBox:Show()
    end
    if statusDrop then
        statusDrop:Show()
    end
    if groupDrop then
        groupDrop:Show()
    end
    if creatorDrop then
        creatorDrop:Show()
    end
end

function SagaloreBoard.hideLogLists()
    if sectionOpen then
        sectionOpen:Hide()
    end
    if sectionDone then
        sectionDone:Hide()
    end
    for i = 1, #rows do
        rows[i]:Hide()
    end
    for i = 1, #goalRows do
        goalRows[i]:Hide()
    end
    for i = 1, #activityRows do
        activityRows[i]:Hide()
    end
    if detailGoalsHeader then
        detailGoalsHeader:Hide()
    end
    if detailActivityHeader then
        detailActivityHeader:Hide()
    end
    if SagaloreBoard.activitySortBtn then
        SagaloreBoard.activitySortBtn:Hide()
    end
    if detailFS then
        detailFS:SetText("")
    end
    if detailImage then
        detailImage:Hide()
    end
    if detailMeta then
        detailMeta:Hide()
    end
    if searchBox then
        searchBox:Hide()
    end
    if statusDrop then
        statusDrop:Hide()
    end
    if groupDrop then
        groupDrop:Hide()
    end
    if creatorDrop then
        creatorDrop:Hide()
    end
    SagaloreBoard.hideCreateURL()
end

function SagaloreBoard.periodLabel(period)
    local periodKey = {
        all = "ui.period_all",
        month = "ui.period_month",
        week = "ui.period_week",
        today = "ui.period_today",
    }
    return L(periodKey[period] or "ui.period_all")
end

function SagaloreBoard.syncBoardLabels()
    if not UIDropDownMenu_SetText then
        return
    end
    local selected = SagaloreBoard.currentBoardGroup()
    if SagaloreBoard.boardGroupDrop then
        local label = L("ui.group")
        if selected then
            label = selected.name or selected.slug or label
        end
        UIDropDownMenu_SetText(SagaloreBoard.boardGroupDrop, label)
    end
    if SagaloreBoard.boardPeriodDrop then
        UIDropDownMenu_SetText(SagaloreBoard.boardPeriodDrop, SagaloreBoard.periodLabel(SagaloreBoard.currentPeriod()))
    end
end

function SagaloreBoard.setupBoardFilters()
    if not UIDropDownMenu_Initialize or not SagaloreBoard.boardGroupDrop or not SagaloreBoard.boardPeriodDrop then
        return
    end
    if SagaloreBoard.filtersReady then
        SagaloreBoard.syncBoardLabels()
        return
    end
    SagaloreBoard.filtersReady = true
    local function pick(text, checked, onPick)
        local info = UIDropDownMenu_CreateInfo()
        info.text = text
        info.checked = checked
        info.padding = 6
        info.func = function()
            onPick()
            SagaloreBoard.pendingRefresh = true
        end
        UIDropDownMenu_AddButton(info)
    end
    UIDropDownMenu_Initialize(SagaloreBoard.boardGroupDrop, function()
        local groups = SagaloreBoard.dataGroups()
        local selected = SagaloreBoard.currentBoardGroup()
        if #groups == 0 then
            pick(L("ui.none"), false, function() end)
            spaceDropList(1)
            return
        end
        for i = 1, #groups do
            local g = groups[i]
            local label = g.name or g.slug or L("ui.group")
            pick(label, selected and selected.id == g.id, function()
                initDB()
                SagaloreDB.board_group = g.id
                refresh()
            end)
        end
        spaceDropList(1)
    end)
    local periodOrder = { "all", "month", "week", "today" }
    UIDropDownMenu_Initialize(SagaloreBoard.boardPeriodDrop, function()
        local current = SagaloreBoard.currentPeriod()
        for i = 1, #periodOrder do
            local key = periodOrder[i]
            pick(SagaloreBoard.periodLabel(key), key == current, function()
                initDB()
                SagaloreDB.board_period = key
                refresh()
            end)
        end
        spaceDropList(1)
    end)
    hookDropListSpacing()
    SagaloreBoard.syncBoardLabels()
end

local CLASS_COLOR = {
    WARRIOR = { 0.78, 0.61, 0.43 },
    PALADIN = { 0.96, 0.55, 0.73 },
    HUNTER = { 0.67, 0.83, 0.45 },
    ROGUE = { 1.00, 0.96, 0.41 },
    PRIEST = { 1.00, 1.00, 1.00 },
    SHAMAN = { 0.00, 0.44, 0.87 },
    MAGE = { 0.41, 0.80, 0.94 },
    WARLOCK = { 0.58, 0.51, 0.79 },
    DRUID = { 1.00, 0.49, 0.04 },
}
local CLASS_TOKEN = {
    warrior = "WARRIOR", kriger = "WARRIOR",
    paladin = "PALADIN",
    hunter = "HUNTER", jeger = "HUNTER",
    rogue = "ROGUE", sniker = "ROGUE", banditt = "ROGUE",
    priest = "PRIEST", prest = "PRIEST",
    shaman = "SHAMAN", sjaman = "SHAMAN",
    mage = "MAGE", magiker = "MAGE",
    warlock = "WARLOCK", heksemester = "WARLOCK",
    druid = "DRUID",
}
local RANK_COLOR = {
    { 1.00, 0.78, 0.20 },
    { 0.82, 0.86, 0.94 },
    { 0.92, 0.55, 0.24 },
}

function SagaloreBoard.classColor(className)
    local token = CLASS_TOKEN[string.lower(className or "")]
    local rgb = token and CLASS_COLOR[token]
    if rgb then
        return rgb[1], rgb[2], rgb[3]
    end
    return 1, 0.95, 0.82
end

function SagaloreBoard.ensureBoardRow(i)
    local row = SagaloreBoard.rows[i]
    if row then
        return row
    end
    row = CreateFrame("Frame", nil, detailChild)
    row:SetHeight(46)
    local bg = row:CreateTexture(nil, "BACKGROUND")
    bg:SetAllPoints()
    bg:SetColorTexture(1, 1, 1, 1)
    row.bg = bg
    local rankFS = row:CreateFontString(nil, "OVERLAY", "GameFontNormalLarge")
    rankFS:SetPoint("LEFT", 8, 1)
    rankFS:SetWidth(28)
    rankFS:SetJustifyH("CENTER")
    row.rankFS = rankFS
    local countFS = row:CreateFontString(nil, "OVERLAY", "GameFontHighlight")
    countFS:SetPoint("RIGHT", -12, 0)
    countFS:SetWidth(108)
    countFS:SetJustifyH("RIGHT")
    row.countFS = countFS
    local nameFS = row:CreateFontString(nil, "OVERLAY", "GameFontNormal")
    nameFS:SetPoint("TOPLEFT", 46, -6)
    nameFS:SetJustifyH("LEFT")
    row.nameFS = nameFS
    local subFS = row:CreateFontString(nil, "OVERLAY", "GameFontHighlightSmall")
    subFS:SetPoint("TOPLEFT", nameFS, "BOTTOMLEFT", 0, -1)
    subFS:SetJustifyH("LEFT")
    subFS:SetTextColor(0.86, 0.78, 0.62)
    row.subFS = subFS
    bindWheel(row, detailScroll, 28)
    SagaloreBoard.rows[i] = row
    return row
end

function SagaloreBoard.refreshBoard()
    if not detailTitle or not detailChild then
        return
    end
    -- A world tick rebuilds this view every second. Repositioning or
    -- reinitializing the dropdowns closes the menu almost immediately.
    if SagaloreBoard.menuOpen() then
        return
    end
    SagaloreBoard.hideLogLists()
    SagaloreBoard.layoutBoardPanels()
    SagaloreBoard.paintViewButtons()
    if SagaloreBoard.boardGroupDrop then
        SagaloreBoard.boardGroupDrop:Show()
    end
    if SagaloreBoard.boardPeriodDrop then
        SagaloreBoard.boardPeriodDrop:Show()
    end
    SagaloreBoard.setupBoardFilters()

    local period = SagaloreBoard.currentPeriod()
    local selected = SagaloreBoard.currentBoardGroup()
    local width = 560
    if detailPanel and detailPanel.GetWidth and detailChild then
        local pw = detailPanel:GetWidth()
        if type(pw) == "number" and pw > 200 then
            detailChild:SetWidth(pw - 48)
            width = pw - 60
        end
    end
    local heading = L("ui.leaderboard")
    if selected then
        heading = selected.name or selected.slug or heading
    end
    detailTitle:SetText(heading)
    detailTitle:SetTextColor(1, 0.86, 0.45)
    detailTitle:SetWidth(width)
    detailTitle:Show()
    local detailY = (detailTitle:GetStringHeight() or 24) + 12

    if SagaloreBoard.reloadHintFS then
        SagaloreBoard.reloadHintFS:ClearAllPoints()
        SagaloreBoard.reloadHintFS:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -detailY)
        SagaloreBoard.reloadHintFS:SetWidth(width)
        SagaloreBoard.reloadHintFS:SetTextColor(0.72, 0.66, 0.52)
        SagaloreBoard.reloadHintFS:SetText(L("ui.board_reload"))
        SagaloreBoard.reloadHintFS:Show()
        detailY = detailY + (SagaloreBoard.reloadHintFS:GetStringHeight() or 14) + 10
    end

    local entries = {}
    local board = selected and SagaloreBoard.findLeaderboard(selected.id, period) or nil
    if type(board) == "table" and type(board.entries) == "table" then
        entries = board.entries
    end
    if #entries == 0 then
        if SagaloreBoard.emptyFS then
            SagaloreBoard.emptyFS:ClearAllPoints()
            SagaloreBoard.emptyFS:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -detailY)
            SagaloreBoard.emptyFS:SetWidth(width)
            SagaloreBoard.emptyFS:SetTextColor(0.9, 0.84, 0.7)
            SagaloreBoard.emptyFS:SetText(L("ui.board_empty"))
            SagaloreBoard.emptyFS:Show()
            detailY = detailY + (SagaloreBoard.emptyFS:GetStringHeight() or 16) + 8
        end
    elseif SagaloreBoard.emptyFS then
        SagaloreBoard.emptyFS:Hide()
    end
    for i = 1, #entries do
        local e = entries[i]
        local row = SagaloreBoard.ensureBoardRow(i)
        local rank = tonumber(e.rank) or i
        local name = plainString(e.name) or ""
        local done = tonumber(e.completed) or 0
        local podium = rank <= 3
        local h = 46
        row:ClearAllPoints()
        row:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 4, -detailY)
        row:SetWidth(width)
        row:SetHeight(h)
        row:Show()
        local medal = RANK_COLOR[rank] or { 0.78, 0.72, 0.55 }
        local you = player.name ~= "" and same(name, player.name)
        if you then
            row.bg:SetVertexColor(1, 0.86, 0.45, 0.16)
        elseif podium then
            row.bg:SetVertexColor(1, 0.9, 0.7, 0.07)
        else
            row.bg:SetVertexColor(1, 1, 1, 0.04)
        end
        row.rankFS:SetText(tostring(rank))
        row.rankFS:SetTextColor(medal[1], medal[2], medal[3])
        local cr, cg, cb = SagaloreBoard.classColor(plainString(e.class) or "")
        row.nameFS:SetText(name)
        row.nameFS:SetTextColor(cr, cg, cb)
        local nameW = width - 180
        if nameW < 80 then
            nameW = 80
        end
        row.nameFS:SetWidth(nameW)
        row.countFS:SetText(string.format(L("ui.board_count"), done))
        row.countFS:SetTextColor(1, 0.9, 0.66)
        local who = ""
        local level = tonumber(e.level) or 0
        if level > 0 then
            who = "Level " .. tostring(level)
        end
        local race = plainString(e.race) or ""
        local className = plainString(e.class) or ""
        if race ~= "" then
            who = who == "" and race or (who .. " " .. race)
        end
        if className ~= "" then
            who = who == "" and className or (who .. " " .. className)
        end
        row.subFS:SetWidth(nameW)
        row.subFS:SetText(who)
        row.subFS:SetTextColor(0.86, 0.78, 0.62)
        detailY = detailY + h + 4
    end
    for i = #entries + 1, #SagaloreBoard.rows do
        SagaloreBoard.rows[i]:Hide()
    end
    detailChild:SetHeight(math.max(detailY + 8, 10))
    if detailScroll and detailScroll.UpdateScrollChildRect then
        detailScroll:UpdateScrollChildRect()
    end
    if updateMinimapUnread then
        updateMinimapUnread()
    end
end

-- Globals, not upvalues. refresh is already at Lua 5.1's upvalue cap.
SagaloreBoard = SagaloreBoard or {}

function SagaloreBoard.begin()
    if SagaloreBoard.currentView() == "board" then
        SagaloreBoard.refreshBoard()
        return false
    end
    SagaloreBoard.showLogChrome()
    SagaloreBoard.paintViewButtons()
    return true
end

function SagaloreBoard.placeActivitySort(y, width)
    local headerW = width
    local headerH = 20
    local btn = SagaloreBoard.activitySortBtn
    local label = SagaloreBoard.activitySortLabel
    if btn then
        if SagaloreBoard.sortMode() == "completion_time" then
            btn:SetText(L("ui.sort_fastest"))
        else
            btn:SetText(L("ui.sort_newest"))
        end
        local btnW = 48
        if btn.sortFS and btn.sortFS.GetStringWidth then
            btnW = btn.sortFS:GetStringWidth() or btnW
        end
        if btnW < 36 then
            btnW = 36
        end
        btn:SetWidth(btnW + 2)
        btn:ClearAllPoints()
        btn:SetPoint("TOPRIGHT", detailChild, "TOPRIGHT", 0, -y)
        btn:Show()
        local labelW = 0
        if label then
            label:SetText(L("ui.sort_by"))
            label:SetTextColor(INK.r, INK.g, INK.b)
            label:ClearAllPoints()
            label:SetPoint("RIGHT", btn, "LEFT", -6, 0)
            label:Show()
            labelW = (label:GetStringWidth() or 48) + 8
        end
        headerW = width - btnW - labelW - 12
        if headerW < 80 then
            headerW = 80
        end
        headerH = 22
    end
    return headerW, headerH
end

function SagaloreBoard.hideActivitySort()
    if SagaloreBoard.activitySortBtn then
        SagaloreBoard.activitySortBtn:Hide()
    end
    if SagaloreBoard.activitySortLabel then
        SagaloreBoard.activitySortLabel:Hide()
    end
end

function SagaloreBoard.urlBoxFocused()
    local box = SagaloreBoard.urlBox
    return box and box.HasFocus and box:HasFocus()
end

function SagaloreBoard.hideCreateURL()
    if SagaloreBoard.urlBox then
        SagaloreBoard.urlBox:ClearFocus()
        SagaloreBoard.urlBox:Hide()
    end
    if SagaloreBoard.urlSelectBtn then
        SagaloreBoard.urlSelectBtn:Hide()
    end
end

function SagaloreBoard.placeCreateURL(y, width, textColor)
    local box = SagaloreBoard.urlBox
    local btn = SagaloreBoard.urlSelectBtn
    if not box then
        return y
    end
    -- World tick rebuilds the detail pane ~1/s. Leave a focused box alone so
    -- the player can select and copy the URL without it clearing.
    if box:IsShown() and SagaloreBoard.urlBoxFocused() then
        return (SagaloreBoard.urlBoxBottomY or (y + 26))
    end
    local btnW = 56
    if btn and btn.GetFontString and btn:GetFontString() and btn:GetFontString().GetStringWidth then
        btnW = math.max(56, (btn:GetFontString():GetStringWidth() or 48) + 16)
    end
    local boxW = width - btnW - 8
    if boxW < 120 then
        boxW = width
        btnW = 0
    end
    box:ClearAllPoints()
    box:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -y)
    box:SetWidth(boxW)
    if box:GetText() ~= CREATE_SAGAS_URL then
        box:SetText(CREATE_SAGAS_URL)
    end
    if type(textColor) == "table" then
        box:SetTextColor(textColor.r or 0.55, textColor.g or 0.75, textColor.b or 1)
    else
        box:SetTextColor(0.45, 0.7, 1)
    end
    box:Show()
    if btn and btnW > 0 then
        btn:ClearAllPoints()
        btn:SetPoint("LEFT", box, "RIGHT", 6, 0)
        btn:SetWidth(btnW)
        btn:Show()
    elseif btn then
        btn:Hide()
    end
    SagaloreBoard.urlBoxBottomY = y + 26
    return SagaloreBoard.urlBoxBottomY
end

refresh = function()
    if not built then
        return
    end
    if not SagaloreBoard.begin() then
        return
    end
    loadFilters()
    if searchBox and searchBox.GetText then
        searchQuery = lowerText(string.gsub(searchBox:GetText() or "", "^%s+", ""))
        searchQuery = string.gsub(searchQuery, "%s+$", "")
    else
        searchQuery = ""
    end
    searchScores = {}
    local entries = currentEntries()

    local openList, doneList = {}, {}
    for i = 1, #entries do
        if passesFilters(entries[i]) then
            if isComplete(entries[i]) then
                table.insert(doneList, entries[i])
            else
                table.insert(openList, entries[i])
            end
        end
    end
    sortBySearch(openList)
    sortBySearch(doneList)

    local selectedEntry = selectedID and findEntry(selectedID) or nil
    -- Prefer the top uncompleted saga. Never auto-select a completed one so
    -- an all-done log can show the info screen instead of a leftover finish.
    if not selectedEntry then
        if openList[1] then
            selectedEntry = openList[1]
            selectedID = selectedEntry.saga_id
        else
            selectedID = nil
        end
    end
    local allDoneInfo = selectedEntry == nil and #openList == 0 and #doneList > 0
    if allDoneInfo then
        SagaloreBoard.applyBoardSkin()
    else
        SagaloreBoard.applyLogSkin()
    end

    local openExpanded = sectionExpanded("open", true)
    local doneExpanded = sectionExpanded("done", true)
    local listY = 0
    local rowIndex = 0
    local selectedRowY = nil

    listY = placeSectionHeader(sectionOpen, listY, string.format("%s (%d)", L("ui.not_started"), #openList), openExpanded, function()
        setSectionExpanded("open", not openExpanded)
        refresh()
    end)
    if openExpanded then
        for i = 1, #openList do
            rowIndex = rowIndex + 1
            local row = ensureRow(rowIndex)
            local item = openList[i]
            row:ClearAllPoints()
            row:SetPoint("TOPLEFT", listChild, "TOPLEFT", 4, -listY)
            row:Show()
            row.questID = item.saga_id
            row.title:SetText(listTitle(item))
            row.sub:SetText(listSubtitle(item, entries))
            layoutListRow(row, isUnread(item.saga_id, item))
            if entryLocked(item, entries) then
                row.title:SetTextColor(LOCKED_TITLE.r, LOCKED_TITLE.g, LOCKED_TITLE.b)
                row.sub:SetTextColor(LOCKED_SUB.r, LOCKED_SUB.g, LOCKED_SUB.b)
            else
                local c = difficultyColor(item.min_level, player.level)
                row.title:SetTextColor(c.r, c.g, c.b)
                row.sub:SetTextColor(0.72, 0.68, 0.58)
            end
            if item.saga_id == selectedID then
                row.bg:SetAlpha(0.7)
                selectedRowY = listY
            else
                row.bg:SetAlpha(0)
            end
            listY = listY + 40
        end
    end

    listY = listY + 6
    listY = placeSectionHeader(sectionDone, listY, string.format("%s (%d)", L("ui.completed_section"), #doneList), doneExpanded, function()
        setSectionExpanded("done", not doneExpanded)
        refresh()
    end)
    if doneExpanded then
        for i = 1, #doneList do
            rowIndex = rowIndex + 1
            local row = ensureRow(rowIndex)
            local item = doneList[i]
            row:ClearAllPoints()
            row:SetPoint("TOPLEFT", listChild, "TOPLEFT", 4, -listY)
            row:Show()
            row.questID = item.saga_id
            row.title:SetText(listTitle(item))
            row.sub:SetText(creatorName(item))
            layoutListRow(row, false)
            row.title:SetTextColor(0.55, 0.75, 0.45)
            row.sub:SetTextColor(0.55, 0.52, 0.45)
            if item.saga_id == selectedID then
                row.bg:SetAlpha(0.55)
                selectedRowY = listY
            else
                row.bg:SetAlpha(0)
            end
            listY = listY + 40
        end
    end

    for i = rowIndex + 1, #rows do
        rows[i]:Hide()
    end
    local listH = math.max(listY + 8, 10)
    listChild:SetHeight(listH)
    if listScroll.UpdateScrollChildRect then
        listScroll:UpdateScrollChildRect()
    end
    local viewH = 384
    if listScroll.GetHeight then
        local h = listScroll:GetHeight()
        if type(h) == "number" and h > 40 then
            viewH = h
        end
    end
    listScroll.range = math.max(0, listH - viewH)
    if scrollSelectIntoView and selectedRowY ~= nil then
        scrollSelectIntoView = false
        local cur = listScroll.offset or 0
        local rowBottom = selectedRowY + 40
        local target = cur
        if selectedRowY < cur then
            target = selectedRowY
        elseif rowBottom > cur + viewH then
            target = rowBottom - viewH
        end
        if target < 0 then
            target = 0
        elseif target > listScroll.range then
            target = listScroll.range
        end
        listScroll.offset = target
        listScroll:SetVerticalScroll(target)
    else
        scrollSelectIntoView = false
    end

    if selectedID ~= lastDetailID then
        lastDetailID = selectedID
        detailScroll.offset = 0
        detailScroll:SetVerticalScroll(0)
    end
    local width = 348
    detailChild:SetWidth(width + 12)
    local detailY = 4
    local goals = {}
    detailFS:Show()
    if not selectedEntry then
        detailTitle:SetText("")
        detailTitle:Hide()
        detailGoalsHeader:Hide()
        if detailImage then
            detailImage:Hide()
        end
        if detailActivityHeader then
            detailActivityHeader:Hide()
        end
        if detailMeta then
            detailMeta:Hide()
        end
        SagaloreBoard.hideActivitySort()
        local showCreateURL = false
        if allDoneInfo then
            -- Use the same FontString as parchment section headers ("Description" /
            -- "Activity") — QuestTitleFont. Avoid SetFont(); on Classic it can
            -- permanently detach the inherit and fall back to body size.
            detailGoalsHeader:Show()
            detailGoalsHeader:SetText(L("ui.all_done"))
            detailGoalsHeader:SetTextColor(1, 0.86, 0.45)
            detailGoalsHeader:SetWidth(width)
            detailGoalsHeader:ClearAllPoints()
            detailGoalsHeader:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -detailY)
            detailY = detailY + (detailGoalsHeader:GetStringHeight() or 20) + 12
            detailFS:SetText(L("ui.all_done_body"))
            detailFS:SetTextColor(0.9, 0.84, 0.7)
            showCreateURL = true
        elseif player.name == "" then
            detailFS:SetText(L("ui.login_character"))
            detailFS:SetTextColor(INK.r, INK.g, INK.b)
        elseif type(SagaloreData) ~= "table" or type(SagaloreData.entries) ~= "table" or #SagaloreData.entries == 0 then
            detailFS:SetText(L("ui.no_sagas"))
            detailFS:SetTextColor(INK.r, INK.g, INK.b)
        elseif #currentEntries() == 0
            and type(SagaloreInvite) == "table" and type(SagaloreInvite.onlyPersonalGroups) == "function"
            and SagaloreInvite.onlyPersonalGroups(SagaloreData) then
            detailFS:SetText(L("ui.ask_invite"))
            detailFS:SetTextColor(INK.r, INK.g, INK.b)
            showCreateURL = true
        else
            detailFS:SetText(string.format(L("ui.no_sagas_for"), player.name))
            detailFS:SetTextColor(INK.r, INK.g, INK.b)
        end
        detailFS:SetWidth(width)
        detailFS:ClearAllPoints()
        detailFS:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -detailY)
        detailY = detailY + (detailFS:GetStringHeight() or 14) + 8
        if showCreateURL then
            local urlColor = allDoneInfo and { r = 0.55, g = 0.78, b = 1 } or { r = 0.2, g = 0.35, b = 0.7 }
            detailY = SagaloreBoard.placeCreateURL(detailY, width, urlColor)
        else
            SagaloreBoard.hideCreateURL()
        end
    else
        SagaloreBoard.hideCreateURL()
        local selectedLocked = entryLocked(selectedEntry, entries)
        detailTitle:Show()
        -- Re-bind the inherit in case a prior SetFont() detached QuestTitleFont.
        if SagaloreBoard.detailTitleFont and _G[SagaloreBoard.detailTitleFont] then
            detailTitle:SetFontObject(_G[SagaloreBoard.detailTitleFont])
        end
        detailTitle:SetText(selectedEntry.title or "Saga")
        -- Parchment header stays dark ink for contrast; list titles carry difficulty color.
        detailTitle:SetTextColor(INK.r, INK.g, INK.b)
        detailTitle:SetWidth(width)
        detailTitle:ClearAllPoints()
        detailTitle:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -detailY)
        detailY = detailY + (detailTitle:GetStringHeight() or 20) + 8

        local meta = ""
        if type(SagaloreChains) == "table" and type(SagaloreChains.detailLines) == "function" then
            meta = SagaloreChains.detailLines(selectedEntry, entries, completionSet()) or ""
        end
        if detailMeta then
            if meta ~= "" then
                detailMeta:Show()
                detailMeta:SetText(meta)
                detailMeta:SetWidth(width)
                detailMeta:ClearAllPoints()
                detailMeta:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -detailY)
                if selectedLocked then
                    detailMeta:SetTextColor(0.45, 0.28, 0.18)
                else
                    detailMeta:SetTextColor(INK_MUTED.r, INK_MUTED.g, INK_MUTED.b)
                end
                detailY = detailY + (detailMeta:GetStringHeight() or 14) + 10
            else
                detailMeta:Hide()
            end
        end

        local okGoals, goalList = pcall(objectiveLines, selectedEntry)
        if okGoals and type(goalList) == "table" then
            goals = goalList
        end
    end

    local selectedLocked = selectedEntry and entryLocked(selectedEntry, entries)
    -- Goals sit under the title with no section header.
    for i, goal in ipairs(goals) do
        local row = ensureGoalRow(i)
        local indent = tonumber(goal.indent) or 0
        if indent < 0 then
            indent = 0
        elseif indent > 3 then
            indent = 3
        end
        local left = 6 + indent * 16
        local rowWidth = width - indent * 16
        local textWidth = rowWidth - 22
        row:ClearAllPoints()
        row:SetPoint("TOPLEFT", detailChild, "TOPLEFT", left, -detailY)
        row:SetWidth(rowWidth)
        row:Show()
        if goal.done then
            row.box:SetTexture("Interface\\AddOns\\Sagalore\\check")
            row.box:SetVertexColor(INK.r, INK.g, INK.b)
            row.text:SetTextColor(INK_MUTED.r, INK_MUTED.g, INK_MUTED.b)
        elseif goal.failed then
            row.box:SetTexture("Interface\\AddOns\\Sagalore\\box")
            row.box:SetVertexColor(0.45, 0.42, 0.38)
            row.text:SetTextColor(0.45, 0.42, 0.38)
        elseif selectedLocked then
            row.box:SetTexture("Interface\\AddOns\\Sagalore\\box")
            row.box:SetVertexColor(0.55, 0.55, 0.55)
            row.text:SetTextColor(INK_MUTED.r, INK_MUTED.g, INK_MUTED.b)
        else
            row.box:SetTexture("Interface\\AddOns\\Sagalore\\box")
            row.box:SetVertexColor(1, 1, 1)
            row.text:SetTextColor(INK.r, INK.g, INK.b)
        end
        row.text:SetWidth(textWidth)
        row.text:SetText(goal.text or "")
        local h = math.max(16, (row.text:GetStringHeight() or 14) + 4)
        row:SetHeight(h)
        detailY = detailY + h + 10
    end
    for i = #goals + 1, #goalRows do
        goalRows[i]:Hide()
    end

    if selectedEntry then
        if #goals > 0 then
            detailY = detailY + 4
        end
        detailGoalsHeader:Show()
        detailGoalsHeader:SetText(L("ui.description"))
        detailGoalsHeader:ClearAllPoints()
        detailGoalsHeader:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -detailY)
        detailGoalsHeader:SetWidth(width)
        detailGoalsHeader:SetTextColor(INK.r, INK.g, INK.b)
        detailY = detailY + (detailGoalsHeader:GetStringHeight() or 20) + 10

        local description = formatDescription(selectedEntry.description)
        detailFS:SetText(description)
        detailFS:SetTextColor(INK.r, INK.g, INK.b)
        detailFS:SetSpacing(4)
        detailFS:SetWidth(width)
        detailFS:ClearAllPoints()
        detailFS:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -detailY)
        detailY = detailY + (detailFS:GetStringHeight() or 14) + 12
        SagaloreBoard.hideCreateURL()

        if detailImage then
            local path = selectedEntry.image
            if type(path) == "string" and path ~= "" then
                detailImage:ClearAllPoints()
                detailImage:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -detailY)
                local aspect = tonumber(selectedEntry.image_aspect) or 1
                if aspect < 0.2 then
                    aspect = 1
                elseif aspect > 5 then
                    aspect = 5
                end
                local imgW = width
                local imgH = math.floor(imgW / aspect + 0.5)
                if imgH > 260 then
                    imgH = 260
                    imgW = math.floor(imgH * aspect + 0.5)
                end
                if imgH < 64 then
                    imgH = 64
                end
                detailImage:SetSize(imgW, imgH)
                detailImage:SetVertexColor(1, 1, 1, 1)
                -- Classic loads AddOn textures as .tga; path is without extension.
                detailImage:SetTexture(path)
                detailImage:Show()
                detailY = detailY + imgH + 14
            else
                detailImage:Hide()
            end
        end
    end

    local activities = {}
    if selectedEntry then
        local okAct, actList = pcall(activityList, selectedEntry)
        if okAct and type(actList) == "table" then
            activities = actList
        end
    end
    local showActivityTiming = selectedEntry and entryHasTimedSteps(selectedEntry)
    if detailActivityHeader then
        if #activities > 0 then
            detailY = detailY + 8
            detailActivityHeader:Show()
            detailActivityHeader:ClearAllPoints()
            detailActivityHeader:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -detailY)
            local headerW = width
            local headerH = detailActivityHeader:GetStringHeight() or 20
            if showActivityTiming then
                local sortH
                headerW, sortH = SagaloreBoard.placeActivitySort(detailY, width)
                if sortH > headerH then
                    headerH = sortH
                end
            else
                SagaloreBoard.hideActivitySort()
            end
            detailActivityHeader:SetWidth(headerW)
            detailActivityHeader:SetTextColor(INK.r, INK.g, INK.b)
            detailY = detailY + headerH + 10
        else
            detailActivityHeader:Hide()
            SagaloreBoard.hideActivitySort()
        end
    end
    for i, act in ipairs(activities) do
        local okRow, errRow = pcall(function()
            local row = ensureActivityRow(i)
            row:ClearAllPoints()
            row:SetPoint("TOPLEFT", detailChild, "TOPLEFT", 6, -detailY)
            row:SetWidth(width)
            row:Show()
            if row.userFS then
                row.userFS:Hide()
            end
            local whenText = act.when or ""
            if showActivityTiming then
                local durText = SagaloreBoard.formatDuration(act.duration_sec)
                if durText ~= "" then
                    if whenText ~= "" then
                        whenText = whenText .. " · " .. durText
                    else
                        whenText = durText
                    end
                end
            end
            local charText = act.character or ""
            local line = charText
            if line ~= "" then
                line = line .. " " .. L("ui.activity_done")
            end
            row.whenFS:SetWidth(width - 16)
            row.charFS:SetWidth(width - 16)
            row.whenFS:SetText(whenText)
            row.charFS:SetText(line)
            row.charFS:SetTextColor(INK.r, INK.g, INK.b)
            row.whenFS:SetTextColor(INK_MUTED.r, INK_MUTED.g, INK_MUTED.b)
            if row.bullet and row.bullet.SetTextColor then
                row.bullet:SetText("•")
                row.bullet:SetTextColor(INK.r, INK.g, INK.b)
            end
            row.tipName = charText
            row.tipLevel = act.level or 0
            row.tipRace = act.race or ""
            row.tipClass = act.class or ""
            row.tipTag = act.battletag or ""
            local h = (row.whenFS:GetStringHeight() or 12) + 2
                + (row.charFS:GetStringHeight() or 14)
            h = math.max(28, h + 4)
            row:SetHeight(h)
            detailY = detailY + h + 10
        end)
        if not okRow then
            chat("[Sagalore] aktivitet: " .. tostring(errRow))
        end
    end
    for i = #activities + 1, #activityRows do
        activityRows[i]:Hide()
    end

    local detailH = math.max(detailY + 8, 10)
    detailChild:SetHeight(detailH)
    if detailScroll.UpdateScrollChildRect then
        detailScroll:UpdateScrollChildRect()
    end
    detailScroll.range = math.max(0, detailH - 410)
    pcall(syncScrollBar, listScroll)
    pcall(syncScrollBar, detailScroll)
    updateMinimapUnread()
end

local function encodeMembers(members)
    local parts = {}
    if type(members) ~= "table" then
        return ""
    end
    for i = 1, #members do
        local m = members[i]
        if type(m) == "table" and type(m.name) == "string" and m.name ~= "" then
            local name = (m.name:gsub("[/;]", ""))
            local realm = ((m.realm or ""):gsub("[/;]", ""))
            table.insert(parts, string.format("%s/%s/%d", name, realm, tonumber(m.level) or 0))
        end
    end
    return table.concat(parts, ";")
end

local function placeLabel(rule, group)
    group = type(group) == "table" and group or {}
    local inst = group.instance or ""
    local zone = group.zone or ""
    if type(rule) ~= "table" or type(rule.instance) ~= "string" or rule.instance == "" then
        if inst ~= "" then
            return inst
        end
        return zone
    end
    if inst ~= "" and SagaloreObjectives and SagaloreObjectives.groupOK then
        local probe = { instance = inst, zone = "", members = { { name = player.name ~= "" and player.name or "x", level = 100 } } }
        if SagaloreObjectives.groupOK({ size = 1, instance = rule.instance }, probe) then
            return inst
        end
    end
    if zone ~= "" then
        return zone
    end
    return inst
end

local function rememberAttest(questID, objID, name, at)
    if type(questID) ~= "string" or questID == "" or type(objID) ~= "string" or objID == "" then
        return
    end
    local who = norm(name)
    if who == "" or not at or at <= 0 then
        return
    end
    if type(world.attests) ~= "table" then
        world.attests = {}
    end
    if type(world.attests[questID]) ~= "table" then
        world.attests[questID] = {}
    end
    if type(world.attests[questID][objID]) ~= "table" then
        world.attests[questID][objID] = {}
    end
    world.attests[questID][objID][who] = at
end

local function sendHerePing(questID, objID)
    if not ((C_ChatInfo and C_ChatInfo.SendAddonMessage) or SendAddonMessage) then
        return
    end
    local raid, grouped = false, false
    pcall(function()
        raid = IsInRaid() and true or false
    end)
    pcall(function()
        grouped = IsInGroup() and true or false
    end)
    local channel
    if raid then
        channel = "RAID"
    elseif grouped then
        channel = "PARTY"
    else
        return
    end
    sendAddon(PREFIX, "H\t" .. tostring(questID) .. "\t" .. tostring(objID), channel)
end

-- A group kill only counts when this client is in the instance at the exact
-- level. The ping lets every other addon attest for itself. Someone outside
-- the dungeon never sends one.
local function witnessGroupKills(deathName)
    if type(deathName) ~= "string" or deathName == "" or not SagaloreObjectives or not SagaloreObjectives.listed then
        return
    end
    cachePlayer()
    if player.name == "" then
        return
    end
    local group = readGroup()
    local now = unixNow()
    for _, entry in ipairs(currentEntries()) do
        if type(entry) == "table" and not isComplete(entry) and type(entry.objectives) == "table" then
            for _, obj in ipairs(entry.objectives) do
                if type(obj) == "table" and obj.type == "kill" and type(obj.group) == "table" and tonumber(obj.group.level) then
                    local level = tonumber(obj.group.level) or 0
                    local inst = obj.group.instance
                    local here = type(inst) ~= "string" or inst == ""
                        or SagaloreObjectives.listed(inst, group.instance)
                        or SagaloreObjectives.listed(inst, group.zone)
                    if here and (tonumber(player.level) or 0) == level and SagaloreObjectives.listed(obj.npc, deathName) then
                        rememberAttest(entry.saga_id, obj.id, player.name, now)
                        sendHerePing(entry.saga_id, obj.id)
                    end
                end
            end
        end
    end
end

local function sendGroupPing(questID, version)
    if not ((C_ChatInfo and C_ChatInfo.SendAddonMessage) or SendAddonMessage) then
        return
    end
    local raid, grouped = false, false
    pcall(function()
        raid = IsInRaid() and true or false
    end)
    pcall(function()
        grouped = IsInGroup() and true or false
    end)
    local channel
    if raid then
        channel = "RAID"
    elseif grouped then
        channel = "PARTY"
    else
        return
    end
    -- Party/raid only, including inside instances. Guild toasts stay outside.
    sendAddon(PREFIX, "G\t" .. tostring(questID) .. "\t" .. tostring(version), channel)
end

finishQuest = function(entry, group)
    if not entry or isComplete(entry) then
        return
    end
    local entries = currentEntries()
    if entryLocked(entry, entries) then
        return
    end
    cachePlayer()
    initDB()
    if player.name == "" then
        return
    end
    local before = completionSet()
    local version = tonumber(entry.saga_version) or 0
    local level = player.level
    if level < 1 then
        level = 1
    end
    local ts = unixNow()
    local row = {
        saga_id = entry.saga_id,
        version = version,
        level = level,
        completed_at = ts,
        character = player.name,
        realm = player.realm,
        race = player.race,
        class = player.class,
    }
    local bucket = SagaloreDB.objectives and SagaloreDB.objectives[entry.saga_id]
    if type(bucket) == "table" then
        local started = tonumber(bucket.started_at) or 0
        if started > 0 then
            row.started_at = started
        end
    end
    if type(entry.group) == "table" and type(group) == "table" then
        row.group_members = encodeMembers(group.members)
        row.instance = placeLabel(entry.group, group)
    end
    SagaloreDB.completions[entry.saga_id] = row
    if type(entry.group) == "table" then
        sendGroupPing(entry.saga_id, version)
    end
    local title = entry.title or "Saga"
    local questID = entry.saga_id
    local unlocked = {}
    if type(SagaloreChains) == "table" and type(SagaloreChains.newlyUnlocked) == "function" then
        unlocked = SagaloreChains.newlyUnlocked(entries, before, completionSet()) or {}
    end
    if unlocked[1] and type(unlocked[1].saga_id) == "string" then
        selectedID = unlocked[1].saga_id
    end
    showSagaToast(title, questID)
    broadcast(string.format("C|%s|%d|%d|%d", questID, version, level, ts))
    refresh()
    if DEFAULT_CHAT_FRAME and DEFAULT_CHAT_FRAME.AddMessage then
        DEFAULT_CHAT_FRAME:AddMessage(
            "|cffc9a227Sagalore|r: " .. L("ui.complete_reload")
        )
        local n = #unlocked
        if n > 3 then
            n = 3
        end
        for i = 1, n do
            DEFAULT_CHAT_FRAME:AddMessage(
                "|cffc9a227Sagalore|r: " .. string.format(L("ui.new_saga"), unlocked[i].title or "Saga")
            )
        end
    end
end

local function onWorld()
    readLocation()
    readNPC()
    readObject()
    checkAll()
    if frame and frame:IsShown() then
        -- Rebuilding the window closes an open dropdown and clears EditBox
        -- selection. Skip while a menu is open or the create-URL box is focused.
        local list = _G.DropDownList1
        if (list and list.IsShown and list:IsShown()) or SagaloreBoard.urlBoxFocused() then
            return
        end
        refresh()
    else
        updateMinimapUnread()
    end
end

local function show()
    if frame then
        frame:Show()
        onWorld()
        if not preserveCompletedSelection then
            local entry = selectedID and findEntry(selectedID) or nil
            if entry and isComplete(entry) then
                local hasOpen = false
                local entries = currentEntries()
                for i = 1, #entries do
                    if not isComplete(entries[i]) then
                        hasOpen = true
                        break
                    end
                end
                if not hasOpen then
                    selectedID = nil
                end
            end
        end
        preserveCompletedSelection = false
        refresh()
    end
end

local function hide()
    if frame then
        frame:Hide()
    end
end

local function toggle()
    if frame and frame:IsShown() then
        hide()
    else
        show()
    end
end

local function wheelDelta(delta)
    local n = plainNumber(delta)
    if n then
        if n > 0 then
            return 1
        end
        if n < 0 then
            return -1
        end
        return 0
    end
    -- Secret/tainted deltas: never branch on the raw value; only recover a plain sign.
    local ok, sign = pcall(function()
        if delta > 0 then
            return 1
        end
        if delta < 0 then
            return -1
        end
        return 0
    end)
    if ok then
        return plainNumber(sign) or 1
    end
    return 1
end

syncScrollBar = function(scroll)
    local bar = scroll and scroll.bar
    if not bar then
        return
    end
    if scroll.UpdateScrollChildRect then
        scroll:UpdateScrollChildRect()
    end
    local range = scroll.range or 0
    if scroll.GetVerticalScrollRange then
        local nativeRange = scroll:GetVerticalScrollRange() or 0
        if nativeRange > range then
            range = nativeRange
        end
    end
    if range < 0 then
        range = 0
    end
    scroll.range = range
    local cur = scroll.offset or 0
    if cur < 0 then
        cur = 0
    elseif cur > range then
        cur = range
    end
    scroll.offset = cur
    scroll:SetVerticalScroll(cur)
    local maxVal = math.max(1, range)
    bar.skip = true
    bar:SetMinMaxValues(0, maxVal)
    -- Force WoW's Slider to place the thumb texture even when cur is 0 on first open.
    bar:SetValue(cur == 0 and maxVal or 0)
    bar:SetValue(cur)
    bar.skip = false
    if bar.thumb then
        if range > 0 then
            bar.thumb:Show()
        else
            bar.thumb:Hide()
        end
    end
end

scrollBy = function(self, delta, step)
    if not self then
        return
    end
    local dir = wheelDelta(delta)
    if dir == 0 then
        return
    end
    if self.UpdateScrollChildRect then
        self:UpdateScrollChildRect()
    end
    local range = self.range or 0
    if self.GetVerticalScrollRange then
        local nativeRange = self:GetVerticalScrollRange() or 0
        if nativeRange > range then
            range = nativeRange
        end
    end
    if range <= 0 then
        return
    end
    local nxt = (self.offset or 0) - dir * (step or 40)
    if nxt < 0 then
        nxt = 0
    elseif nxt > range then
        nxt = range
    end
    self.offset = nxt
    -- Drive the slider when present so thumb + OnValueChanged stay aligned.
    local bar = self.bar
    if bar then
        bar.skip = true
        bar:SetMinMaxValues(0, math.max(1, range))
        bar:SetValue(nxt == 0 and math.max(1, range) or 0)
        bar:SetValue(nxt)
        bar.skip = false
    end
    self:SetVerticalScroll(nxt)
    syncScrollBar(self)
end

bindWheel = function(target, scroll, step)
    if not target then
        return
    end
    pcall(function()
        if target.EnableMouseWheel then
            target:EnableMouseWheel(true)
        end
        target:SetScript("OnMouseWheel", function(_, delta)
            scrollBy(scroll, delta, step)
        end)
    end)
end

wheelOnFrame = function(_, delta)
    local scale = frame and frame:GetEffectiveScale() or 1
    if scale == 0 then
        scale = 1
    end
    local cx = GetCursorPosition()
    cx = (plainNumber(cx) or 0) / scale
    local left = detailPanel and detailPanel:GetLeft()
    left = plainNumber(left)
    if left and cx >= left then
        scrollBy(detailScroll, delta, 28)
    else
        scrollBy(listScroll, delta, 36)
    end
end

local function makeScrollArrow(parent, up)
    local btn = CreateFrame("Button", nil, parent)
    btn:SetSize(18, 16)
    local tex = btn:CreateTexture(nil, "ARTWORK")
    tex:SetPoint("TOPLEFT", -4, 4)
    tex:SetPoint("BOTTOMRIGHT", 4, -4)
    if up then
        tex:SetTexture("Interface\\Buttons\\UI-ScrollBar-ScrollUpButton-Up")
    else
        tex:SetTexture("Interface\\Buttons\\UI-ScrollBar-ScrollDownButton-Up")
    end
    btn.tex = tex
    return btn
end

local function attachScrollBar(parent, scroll, step)
    local up = makeScrollArrow(parent, true)
    local down = makeScrollArrow(parent, false)
    up:SetPoint("TOPRIGHT", parent, "TOPRIGHT", -5, -6)
    down:SetPoint("BOTTOMRIGHT", parent, "BOTTOMRIGHT", -5, 6)
    local bar = CreateFrame("Slider", nil, parent)
    bar:SetOrientation("VERTICAL")
    bar:SetWidth(16)
    bar:SetPoint("TOP", up, "BOTTOM", 0, -2)
    bar:SetPoint("BOTTOM", down, "TOP", 0, 2)
    bar:SetMinMaxValues(0, 1)
    bar:SetValueStep(1)
    local trackBorder = bar:CreateTexture(nil, "BACKGROUND")
    trackBorder:SetPoint("TOPLEFT", 2, 2)
    trackBorder:SetPoint("BOTTOMRIGHT", -2, -2)
    trackBorder:SetColorTexture(0.38, 0.28, 0.14, 0.9)
    local track = bar:CreateTexture(nil, "BORDER")
    track:SetPoint("TOPLEFT", 3, 1)
    track:SetPoint("BOTTOMRIGHT", -3, -1)
    track:SetColorTexture(0.08, 0.05, 0.03, 0.95)
    bar:SetThumbTexture("Interface\\Buttons\\UI-ScrollBar-Knob")
    local thumb = bar:GetThumbTexture()
    if thumb then
        thumb:SetSize(24, 24)
    end
    bar.thumb = thumb
    bar:EnableMouse(true)
    bar:EnableMouseWheel(true)
    bar:SetScript("OnValueChanged", function(self, value)
        if self.skip then
            return
        end
        local v = value or 0
        scroll.offset = v
        if scroll.UpdateScrollChildRect then
            scroll:UpdateScrollChildRect()
        end
        scroll:SetVerticalScroll(v)
    end)
    scroll:SetScript("OnScrollRangeChanged", function(self)
        syncScrollBar(self)
    end)
    bar:SetScript("OnMouseWheel", function(_, delta)
        scrollBy(scroll, delta, step)
    end)
    up:EnableMouseWheel(true)
    up:SetScript("OnClick", function()
        scrollBy(scroll, 1, step)
    end)
    up:SetScript("OnMouseWheel", function(_, delta)
        scrollBy(scroll, delta, step)
    end)
    down:EnableMouseWheel(true)
    down:SetScript("OnClick", function()
        scrollBy(scroll, -1, step)
    end)
    down:SetScript("OnMouseWheel", function(_, delta)
        scrollBy(scroll, delta, step)
    end)
    bar.up = up
    bar.down = down
    scroll.bar = bar
    return bar
end

local function useBackdrop(f, info, border)
    if BackdropTemplateMixin and not f.SetBackdrop then
        Mixin(f, BackdropTemplateMixin)
    end
    if f.SetBackdrop then
        f:SetBackdrop(info)
    end
    if border and f.SetBackdropBorderColor then
        f:SetBackdropBorderColor(border.r, border.g, border.b, 1)
    end
end

local function makeSectionHeader(parent)
    local btn = CreateFrame("Button", nil, parent)
    btn:SetHeight(22)
    local bg = btn:CreateTexture(nil, "BACKGROUND")
    bg:SetAllPoints()
    bg:SetColorTexture(0.18, 0.12, 0.05, 0.85)
    btn.bg = bg
    local icon = btn:CreateTexture(nil, "ARTWORK")
    icon:SetSize(16, 16)
    icon:SetPoint("LEFT", 4, 0)
    icon:SetTexture("Interface\\Buttons\\UI-MinusButton-UP")
    btn.icon = icon
    local highlight = btn:CreateTexture(nil, "HIGHLIGHT")
    highlight:SetAllPoints(icon)
    highlight:SetTexture("Interface\\Buttons\\UI-PlusButton-Hilight")
    highlight:SetBlendMode("ADD")
    local label = btn:CreateFontString(nil, "OVERLAY", "GameFontNormal")
    label:SetPoint("LEFT", icon, "RIGHT", 4, 0)
    label:SetTextColor(GOLD.r, GOLD.g, GOLD.b)
    btn.label = label
    bindWheel(btn, listScroll, 36)
    return btn
end

-- Same bottom tabs as the character / social window.
-- Minimap is created before build(), so a template error cannot hide the icon.
local function makeBottomTab(parent, id, label)
    local name = parent:GetName() and (parent:GetName() .. "Tab" .. id) or nil
    local tab = CreateFrame("Button", name, parent, "CharacterFrameTabButtonTemplate")
    tab:SetID(id)
    tab:SetText(label)
    if PanelTemplates_TabResize then
        -- Era uses (tab, padding). Older clients used (padding, tab).
        if not pcall(PanelTemplates_TabResize, tab, 0) then
            pcall(PanelTemplates_TabResize, 0, tab)
        end
    end
    SagaloreBoard.tintTab(tab)
    return tab
end

local function ensureMinimapButton()
    if minimapBtn or not Minimap then
        return
    end
    local btn = CreateFrame("Button", "SagaloreMinimapButton", Minimap)
    minimapBtn = btn
    -- Larger hit/visual size so the emblem is not crushed into ~20px.
    btn:SetSize(40, 40)
    btn:SetFrameStrata("MEDIUM")
    btn:RegisterForClicks("LeftButtonUp")
    btn:RegisterForDrag("LeftButton")
    btn.angle = math.pi * 0.75
    if type(SagaloreDB) == "table" and type(SagaloreDB.minimapAngle) == "number" then
        btn.angle = SagaloreDB.minimapAngle
    end
    -- Unread: soft blue ADD glow (Blizzard zoom highlight).
    local glow = btn:CreateTexture(nil, "BACKGROUND")
    glow:SetSize(58, 58)
    glow:SetPoint("CENTER")
    glow:SetTexture("Interface\\Minimap\\UI-Minimap-ZoomButton-Highlight")
    glow:SetBlendMode("ADD")
    glow:SetVertexColor(0.42, 0.72, 1.0)
    glow:SetAlpha(1)
    glow:Hide()
    btn.unreadGlow = glow
    -- LibDBIcon TrackingBorder layout, scaled so the icon stays ~30px
    -- (small holes look crunchy with the ornate gold rim).
    local icon = btn:CreateTexture(nil, "ARTWORK")
    icon:SetSize(30, 30)
    icon:SetPoint("TOPLEFT", 6, -5)
    icon:SetTexture("Interface\\AddOns\\Sagalore\\SagaloreMinimap")
    local border = btn:CreateTexture(nil, "OVERLAY")
    border:SetSize(66, 66)
    border:SetPoint("TOPLEFT")
    border:SetTexture("Interface\\Minimap\\MiniMap-TrackingBorder")
    local function placeMinimap(self)
        local angle = self.angle or (math.pi * 0.75)
        local radius = (Minimap:GetWidth() / 2) + 8
        self:ClearAllPoints()
        self:SetPoint("CENTER", Minimap, "CENTER", math.cos(angle) * radius, math.sin(angle) * radius)
    end
    placeMinimap(btn)
    btn:SetScript("OnDragStart", function(self)
        self.dragging = true
        self.moved = true
    end)
    btn:SetScript("OnDragStop", function(self)
        self.dragging = false
        initDB()
        SagaloreDB.minimapAngle = self.angle
    end)
    btn:SetScript("OnUpdate", function(self)
        if not self.dragging then
            return
        end
        local mx, my = Minimap:GetCenter()
        local scale = Minimap:GetEffectiveScale()
        if not mx or not scale or scale == 0 then
            return
        end
        local cx, cy = GetCursorPosition()
        cx, cy = cx / scale, cy / scale
        self.angle = math.atan2(cy - my, cx - mx)
        placeMinimap(self)
    end)
    btn:SetScript("OnClick", function(self)
        if self.moved then
            self.moved = false
            return
        end
        toggle()
    end)
    btn:SetScript("OnEnter", function(self)
        if not GameTooltip then
            return
        end
        GameTooltip:SetOwner(self, "ANCHOR_LEFT")
        GameTooltip:SetText("Sagalore")
        local unreadLine = unreadTooltipLine()
        if unreadLine then
            GameTooltip:AddLine(unreadLine, UNREAD_BLUE.r, UNREAD_BLUE.g, UNREAD_BLUE.b)
        end
        GameTooltip:AddLine(L("ui.drag_minimap"), 0.9, 0.8, 0.6)
        GameTooltip:Show()
    end)
    btn:SetScript("OnLeave", function()
        if GameTooltip then
            GameTooltip:Hide()
        end
    end)
    updateMinimapUnread()
end

local function build()
    if built then
        ensureMinimapButton()
        return
    end
    -- Minimap first so a later UI error cannot leave the player without the icon.
    ensureMinimapButton()
    local backdropTemplate = BackdropTemplateMixin and "BackdropTemplate" or nil
    frame = CreateFrame("Frame", "SagaloreFrame", UIParent, backdropTemplate)
    frame:SetSize(700, 500)
    frame:SetPoint("CENTER")
    frame:SetFrameStrata("HIGH")
    frame:SetMovable(true)
    frame:SetClampedToScreen(true)
    frame:EnableMouse(true)
    frame:EnableMouseWheel(true)
    frame:SetScript("OnMouseWheel", wheelOnFrame)
    frame:Hide()
    table.insert(UISpecialFrames, "SagaloreFrame")
    useBackdrop(frame, {
        bgFile = "Interface\\DialogFrame\\UI-DialogBox-Background",
        edgeFile = "Interface\\DialogFrame\\UI-DialogBox-Border",
        tile = true,
        tileSize = 32,
        edgeSize = 32,
        insets = { left = 11, right = 12, top = 12, bottom = 11 },
    }, GOLD)

    local header = frame:CreateTexture(nil, "ARTWORK")
    header:SetTexture("Interface\\DialogFrame\\UI-DialogBox-Header")
    header:SetSize(300, 64)
    header:SetPoint("TOP", frame, "TOP", 0, 12)
    header:SetVertexColor(GOLD.r, GOLD.g, GOLD.b)
    local title = frame:CreateFontString(nil, "OVERLAY", "GameFontNormal")
    title:SetPoint("TOP", header, "TOP", 0, -14)
    title:SetText("Sagalore")
    -- Yellower than the border gold so the header reads as lit metal.
    title:SetTextColor(1.0, 0.82, 0.12)

    local mark = frame:CreateTexture(nil, "OVERLAY")
    mark:SetSize(58, 58)
    mark:SetPoint("TOPLEFT", frame, "TOPLEFT", -10, 14)
    mark:SetTexture("Interface\\AddOns\\Sagalore\\Sagalore")

    local close = CreateFrame("Button", nil, frame, "UIPanelCloseButton")
    close:SetPoint("TOPRIGHT", frame, "TOPRIGHT", -2, -2)

    -- Bottom tabs (Social / Friends style). The active tab is lifted so it
    -- covers the dialog border and reads as part of the page.
    SagaloreBoard.sagasTab = makeBottomTab(frame, 1, L("ui.sagas"))
    SagaloreBoard.sagasTab:SetScript("OnClick", function()
        initDB()
        SagaloreDB.view = "log"
        refresh()
    end)
    SagaloreBoard.boardTab = makeBottomTab(frame, 2, L("ui.leaderboard"))
    SagaloreBoard.boardTab:SetScript("OnClick", function()
        initDB()
        SagaloreDB.view = "board"
        refresh()
    end)
    frame.numTabs = 2
    if PanelTemplates_SetNumTabs then
        PanelTemplates_SetNumTabs(frame, 2)
    end
    if PanelTemplates_SetTab then
        PanelTemplates_SetTab(frame, 1)
    end
    SagaloreBoard.placeTabs(1)
    frame:SetScript("OnMouseDown", function()
        blurSearch()
    end)

    local drag = CreateFrame("Frame", nil, frame)
    drag:SetPoint("TOPLEFT", frame, "TOPLEFT", 56, 10)
    drag:SetPoint("TOPRIGHT", frame, "TOPRIGHT", -46, 10)
    drag:SetHeight(28)
    drag:EnableMouse(true)
    drag:EnableMouseWheel(true)
    drag:SetScript("OnMouseWheel", wheelOnFrame)
    drag:RegisterForDrag("LeftButton")
    drag:SetScript("OnMouseDown", function()
        blurSearch()
    end)
    drag:SetScript("OnDragStart", function()
        frame:StartMoving()
    end)
    drag:SetScript("OnDragStop", function()
        frame:StopMovingOrSizing()
        local point, _, rel, x, y = frame:GetPoint()
        if type(SagaloreDB) == "table" and type(point) == "string" and type(rel) == "string" then
            SagaloreDB.point = { point, rel, x, y }
        end
    end)

    if type(SagaloreDB) == "table" and type(SagaloreDB.point) == "table" then
        frame:ClearAllPoints()
        frame:SetPoint(SagaloreDB.point[1], UIParent, SagaloreDB.point[2], SagaloreDB.point[3], SagaloreDB.point[4])
    end

    listPanel = CreateFrame("Frame", nil, frame, backdropTemplate)
    listPanel:SetPoint("TOPLEFT", 22, -72)
    listPanel:SetPoint("BOTTOMLEFT", 22, 22)
    listPanel:SetSize(236, 410)
    useBackdrop(listPanel, {
        bgFile = "Interface\\Buttons\\WHITE8X8",
        edgeFile = "Interface\\Tooltips\\UI-Tooltip-Border",
        tile = true,
        tileSize = 8,
        edgeSize = 12,
        insets = { left = 3, right = 3, top = 3, bottom = 3 },
    }, GOLD)
    if listPanel.SetBackdropColor then
        listPanel:SetBackdropColor(0.07, 0.05, 0.03, 0.92)
    end

    if UIDropDownMenu_Initialize then
        groupDrop = CreateFrame("Frame", "SagaloreGroupFilter", frame, "UIDropDownMenuTemplate")
        groupDrop:SetPoint("BOTTOMLEFT", listPanel, "TOPLEFT", -14, -4)
        UIDropDownMenu_SetWidth(groupDrop, 96)
        UIDropDownMenu_JustifyText(groupDrop, "LEFT")
        creatorDrop = CreateFrame("Frame", "SagaloreCreatorFilter", frame, "UIDropDownMenuTemplate")
        creatorDrop:SetPoint("LEFT", groupDrop, "RIGHT", -16, 0)
        UIDropDownMenu_SetWidth(creatorDrop, 110)
        UIDropDownMenu_JustifyText(creatorDrop, "LEFT")
        setupListFilters()
    end
    statusDrop = nil

    listScroll = CreateFrame("ScrollFrame", "SagaloreListScroll", listPanel)
    listScroll:SetPoint("TOPLEFT", 6, -8)
    listScroll:SetSize(202, 394)
    listScroll:SetPoint("BOTTOMRIGHT", -28, 8)
    listChild = CreateFrame("Frame", "SagaloreListChild", listScroll)
    listChild:SetSize(196, 418)
    listScroll:SetScrollChild(listChild)
    listPanel:EnableMouse(true)
    listScroll:EnableMouse(true)
    listChild:EnableMouse(true)
    listPanel:SetScript("OnMouseDown", blurSearch)
    listScroll:SetScript("OnMouseDown", blurSearch)
    bindWheel(listPanel, listScroll, 36)
    bindWheel(listScroll, listScroll, 36)
    bindWheel(listChild, listScroll, 36)
    sectionOpen = makeSectionHeader(listChild)
    sectionDone = makeSectionHeader(listChild)
    attachScrollBar(listPanel, listScroll, 36)

    -- Same top and bottom as the sidebar. Explicit size keeps ScrollFrame
    -- clipping valid even when refreshed before the window is first shown.
    detailPanel = CreateFrame("Frame", nil, frame, backdropTemplate)
    detailPanel:SetPoint("TOPLEFT", listPanel, "TOPRIGHT", 14, 0)
    detailPanel:SetPoint("BOTTOMRIGHT", frame, "BOTTOMRIGHT", -18, 22)
    detailPanel:SetSize(410, 410)
    useBackdrop(detailPanel, {
        bgFile = "Interface\\Buttons\\WHITE8X8",
        edgeFile = "Interface\\Tooltips\\UI-Tooltip-Border",
        tile = true,
        tileSize = 8,
        edgeSize = 14,
        insets = { left = 4, right = 4, top = 4, bottom = 4 },
    }, GOLD)
    if detailPanel.SetBackdropColor then
        detailPanel:SetBackdropColor(0.86, 0.78, 0.58, 0.97)
    end

    if UIDropDownMenu_Initialize then
        -- Leaderboard filters: same dropdown style as the saga list filters.
        SagaloreBoard.boardGroupDrop = CreateFrame("Frame", "SagaloreBoardGroupFilter", frame, "UIDropDownMenuTemplate")
        SagaloreBoard.boardGroupDrop:SetPoint("BOTTOMLEFT", detailPanel, "TOPLEFT", -14, -4)
        UIDropDownMenu_SetWidth(SagaloreBoard.boardGroupDrop, 150)
        UIDropDownMenu_JustifyText(SagaloreBoard.boardGroupDrop, "LEFT")
        SagaloreBoard.boardPeriodDrop = CreateFrame("Frame", "SagaloreBoardPeriodFilter", frame, "UIDropDownMenuTemplate")
        SagaloreBoard.boardPeriodDrop:SetPoint("LEFT", SagaloreBoard.boardGroupDrop, "RIGHT", -8, 0)
        UIDropDownMenu_SetWidth(SagaloreBoard.boardPeriodDrop, 110)
        UIDropDownMenu_JustifyText(SagaloreBoard.boardPeriodDrop, "LEFT")
        SagaloreBoard.boardGroupDrop:Hide()
        SagaloreBoard.boardPeriodDrop:Hide()
        SagaloreBoard.setupBoardFilters()
    end

    searchBox = CreateFrame("EditBox", "SagaloreSearchBox", frame, "InputBoxTemplate")
    searchBox:SetAutoFocus(false)
    searchBox:SetHeight(20)
    searchBox:SetWidth(136)
    searchBox:SetPoint("BOTTOMRIGHT", detailPanel, "TOPRIGHT", -6, 6)
    searchBox:SetFontObject(GameFontHighlightSmall)
    searchBox:SetTextInsets(8, 8, 0, 0)
    searchBox:SetMaxLetters(80)
    searchBox:SetScript("OnEscapePressed", function(self)
        if (self:GetText() or "") ~= "" then
            self:SetText("")
            refresh()
        end
        self:ClearFocus()
        updateSearchHint()
    end)
    searchBox:SetScript("OnEnterPressed", function(self)
        self:ClearFocus()
        updateSearchHint()
        refresh()
    end)
    searchBox:SetScript("OnTextChanged", function(_, userInput)
        updateSearchHint()
        if userInput then
            refresh()
        end
    end)
    searchBox:SetScript("OnEditFocusGained", function()
        updateSearchHint()
    end)
    searchBox:SetScript("OnEditFocusLost", function()
        updateSearchHint()
    end)
    local searchHint = searchBox:CreateFontString(nil, "OVERLAY", "GameFontDisableSmall")
    searchHint:SetPoint("LEFT", searchBox, "LEFT", 10, 0)
    searchHint:SetText(L("ui.search"))
    searchBox.hint = searchHint
    updateSearchHint()

    local parchment = detailPanel:CreateTexture(nil, "BACKGROUND", nil, 1)
    parchment:SetPoint("TOPLEFT", 5, -5)
    parchment:SetPoint("BOTTOMRIGHT", -5, 5)
    parchment:SetTexture("Interface\\QuestFrame\\QuestBG")
    parchment:SetTexCoord(0, 0.58, 0.02, 0.66)
    parchment:SetVertexColor(1, 0.97, 0.9)
    detailPanel.parchment = parchment

    detailScroll = CreateFrame("ScrollFrame", "SagaloreDetailScroll", detailPanel)
    detailScroll:SetPoint("TOPLEFT", 12, -12)
    detailScroll:SetPoint("BOTTOMRIGHT", -30, 12)
    detailScroll:SetSize(368, 410)
    detailChild = CreateFrame("Frame", "SagaloreDetailChild", detailScroll)
    detailChild:SetSize(360, 410)
    detailScroll:SetScrollChild(detailChild)
    detailPanel:EnableMouse(true)
    detailScroll:EnableMouse(true)
    detailChild:EnableMouse(true)
    detailPanel:SetScript("OnMouseDown", blurSearch)
    detailScroll:SetScript("OnMouseDown", blurSearch)
    bindWheel(detailPanel, detailScroll, 28)
    bindWheel(detailScroll, detailScroll, 28)
    bindWheel(detailChild, detailScroll, 28)
    local titleFont = "GameFontNormalLarge"
    if QuestTitleFont then
        titleFont = "QuestTitleFont"
    end
    SagaloreBoard.detailTitleFont = titleFont
    detailTitle = detailChild:CreateFontString(nil, "OVERLAY", titleFont)
    detailTitle:SetPoint("TOPLEFT", 6, -4)
    detailTitle:SetJustifyH("LEFT")
    detailTitle:SetJustifyV("TOP")
    detailTitle:SetWordWrap(true)
    detailTitle:SetTextColor(INK.r, INK.g, INK.b)
    detailMeta = detailChild:CreateFontString(nil, "OVERLAY", "GameFontHighlight")
    detailMeta:SetJustifyH("LEFT")
    detailMeta:SetJustifyV("TOP")
    detailMeta:SetWordWrap(true)
    detailMeta:SetSpacing(2)
    detailMeta:SetTextColor(INK_MUTED.r, INK_MUTED.g, INK_MUTED.b)
    detailMeta:Hide()
    detailFS = detailChild:CreateFontString(nil, "OVERLAY", "GameFontHighlight")
    detailFS:SetPoint("TOPLEFT", detailTitle, "BOTTOMLEFT", 0, -10)
    detailFS:SetJustifyH("LEFT")
    detailFS:SetJustifyV("TOP")
    detailFS:SetWordWrap(true)
    detailFS:SetSpacing(2)
    detailFS:SetTextColor(INK.r, INK.g, INK.b)
    if detailFS.SetHyperlinksEnabled then
        detailFS:SetHyperlinksEnabled(true)
    end
    detailImage = detailChild:CreateTexture(nil, "ARTWORK")
    detailImage:SetTexCoord(0, 1, 0, 1)
    detailImage:SetVertexColor(1, 1, 1, 1)
    detailImage:Hide()
    detailGoalsHeader = detailChild:CreateFontString(nil, "OVERLAY", titleFont)
    detailGoalsHeader:SetJustifyH("LEFT")
    detailGoalsHeader:SetText(L("ui.description"))
    detailGoalsHeader:SetTextColor(INK.r, INK.g, INK.b)
    detailActivityHeader = detailChild:CreateFontString(nil, "OVERLAY", titleFont)
    detailActivityHeader:SetJustifyH("LEFT")
    detailActivityHeader:SetText(L("ui.activity"))
    detailActivityHeader:SetTextColor(INK.r, INK.g, INK.b)
    detailActivityHeader:Hide()
    SagaloreBoard.activitySortLabel = detailChild:CreateFontString(nil, "OVERLAY", "GameFontHighlightSmall")
    SagaloreBoard.activitySortLabel:SetJustifyH("RIGHT")
    SagaloreBoard.activitySortLabel:SetText(L("ui.sort_by"))
    SagaloreBoard.activitySortLabel:SetTextColor(INK.r, INK.g, INK.b)
    SagaloreBoard.activitySortLabel:Hide()
    SagaloreBoard.activitySortBtn = CreateFrame("Button", nil, detailChild)
    SagaloreBoard.activitySortBtn:SetSize(72, 18)
    local sortFS = SagaloreBoard.activitySortBtn:CreateFontString(nil, "OVERLAY", "GameFontHighlightSmall")
    sortFS:SetPoint("RIGHT", 0, 0)
    sortFS:SetJustifyH("RIGHT")
    sortFS:SetTextColor(0.45, 0.32, 0.16)
    SagaloreBoard.activitySortBtn:SetFontString(sortFS)
    SagaloreBoard.activitySortBtn.sortFS = sortFS
    SagaloreBoard.activitySortBtn:SetText(L("ui.sort_newest"))
    SagaloreBoard.activitySortBtn:SetScript("OnEnter", function(self)
        if self.sortFS then
            self.sortFS:SetTextColor(INK.r, INK.g, INK.b)
        end
    end)
    SagaloreBoard.activitySortBtn:SetScript("OnLeave", function(self)
        if self.sortFS then
            self.sortFS:SetTextColor(0.45, 0.32, 0.16)
        end
    end)
    SagaloreBoard.activitySortBtn:Hide()
    SagaloreBoard.activitySortBtn:SetScript("OnClick", function()
        initDB()
        if SagaloreBoard.sortMode() == "completion_time" then
            SagaloreDB.activity_sort = "completed_at"
        else
            SagaloreDB.activity_sort = "completion_time"
        end
        refresh()
    end)
    SagaloreBoard.emptyFS = detailChild:CreateFontString(nil, "OVERLAY", "GameFontHighlight")
    SagaloreBoard.emptyFS:SetJustifyH("LEFT")
    SagaloreBoard.emptyFS:SetJustifyV("TOP")
    SagaloreBoard.emptyFS:SetWordWrap(true)
    SagaloreBoard.emptyFS:SetTextColor(INK.r, INK.g, INK.b)
    SagaloreBoard.emptyFS:Hide()
    SagaloreBoard.reloadHintFS = detailChild:CreateFontString(nil, "OVERLAY", "GameFontHighlightSmall")
    SagaloreBoard.reloadHintFS:SetJustifyH("LEFT")
    SagaloreBoard.reloadHintFS:SetJustifyV("TOP")
    SagaloreBoard.reloadHintFS:SetWordWrap(true)
    SagaloreBoard.reloadHintFS:SetTextColor(0.72, 0.66, 0.52)
    SagaloreBoard.reloadHintFS:Hide()
    local urlBox = CreateFrame("EditBox", "SagaloreCreateURLBox", detailChild, "InputBoxTemplate")
    urlBox:SetAutoFocus(false)
    urlBox:SetHeight(22)
    urlBox:SetMaxLetters(120)
    urlBox:SetTextInsets(8, 8, 0, 0)
    urlBox:SetScript("OnEscapePressed", function(self)
        self:ClearFocus()
    end)
    urlBox:SetScript("OnEditFocusGained", function(self)
        self:HighlightText(0, -1)
    end)
    urlBox:Hide()
    SagaloreBoard.urlBox = urlBox
    local urlBtn = CreateFrame("Button", nil, detailChild, "UIPanelButtonTemplate")
    urlBtn:SetSize(64, 22)
    urlBtn:SetText(L("ui.copy_link"))
    urlBtn:SetScript("OnClick", function()
        local box = SagaloreBoard.urlBox
        if not box then
            return
        end
        box:SetFocus()
        box:HighlightText(0, -1)
    end)
    urlBtn:Hide()
    SagaloreBoard.urlSelectBtn = urlBtn
    pcall(function()
        detailFS:SetScript("OnHyperlinkClick", onGoalLinkClick)
        detailFS:SetScript("OnHyperlinkEnter", onGoalLinkEnter)
        detailFS:SetScript("OnHyperlinkLeave", onGoalLinkLeave)
    end)
    bindWheel(detailTitle, detailScroll, 28)
    bindWheel(detailMeta, detailScroll, 28)
    bindWheel(detailFS, detailScroll, 28)
    bindWheel(detailGoalsHeader, detailScroll, 28)
    bindWheel(detailActivityHeader, detailScroll, 28)
    if SagaloreBoard.reloadHintFS then
        bindWheel(SagaloreBoard.reloadHintFS, detailScroll, 28)
    end
    attachScrollBar(detailPanel, detailScroll, 28)

    toast = CreateFrame("Frame", "SagaloreToast", UIParent, backdropTemplate)
    toast:SetSize(390, 92)
    toast:SetFrameStrata("DIALOG")
    toast:SetPoint("TOP", UIParent, "TOP", 0, -30)
    toast:Hide()
    useBackdrop(toast, {
        bgFile = "Interface\\DialogFrame\\UI-DialogBox-Background",
        edgeFile = "Interface\\DialogFrame\\UI-DialogBox-Border",
        tile = true,
        tileSize = 32,
        edgeSize = 24,
        insets = { left = 6, right = 6, top = 6, bottom = 6 },
    }, GOLD)
    local toastIcon = toast:CreateTexture(nil, "ARTWORK")
    toastIcon:SetSize(58, 58)
    toastIcon:SetPoint("LEFT", 18, 0)
    toastIcon:SetTexture("Interface\\AddOns\\Sagalore\\Sagalore")
    local toastHeading = toast:CreateFontString(nil, "OVERLAY", "GameFontNormal")
    toastHeading:SetPoint("TOPLEFT", toastIcon, "TOPRIGHT", 14, -8)
    toastHeading:SetText(L("ui.saga_completed"))
    local toastTitle = toast:CreateFontString(nil, "OVERLAY", "GameFontNormalLarge")
    toastTitle:SetPoint("TOPLEFT", toastHeading, "BOTTOMLEFT", 0, -4)
    toastTitle:SetPoint("RIGHT", toast, "RIGHT", -18, 0)
    toastTitle:SetJustifyH("LEFT")
    toastTitle:SetWordWrap(false)
    toastTitle:SetTextColor(1, 0.95, 0.8)
    -- Ease in (~0.35s), hold longer, then a simple fade (no slide-out).
    local TOAST_EASE = 0.35
    local TOAST_HOLD = 7.0
    local TOAST_FADE = 0.55
    toast:SetScript("OnUpdate", function(self, elapsed)
        if not self:IsShown() then
            return
        end
        self.age = (self.age or 0) + (elapsed or 0)
        local age = self.age
        local alpha, y
        if age < TOAST_EASE then
            local t = age / TOAST_EASE
            alpha = t
            y = 16 - 46 * t
        elseif age < TOAST_HOLD then
            alpha = 1
            y = -30
        elseif age < TOAST_HOLD + TOAST_FADE then
            local t = (age - TOAST_HOLD) / TOAST_FADE
            alpha = 1 - t
            y = -30
        else
            self:Hide()
            return
        end
        self:SetAlpha(alpha)
        self:ClearAllPoints()
        self:SetPoint("TOP", UIParent, "TOP", 0, y)
    end)
    toast:EnableMouse(true)
    toast:SetScript("OnMouseUp", function(self, button)
        if button ~= "LeftButton" then
            return
        end
        local questID = self.questID
        self:Hide()
        if type(questID) == "string" and questID ~= "" then
            preserveCompletedSelection = true
            selectedID = questID
            markRead(questID)
            local entry = findEntry(questID)
            if isComplete(entry) then
                setSectionExpanded("done", true)
            else
                setSectionExpanded("open", true)
            end
            scrollSelectIntoView = true
            initDB()
            SagaloreDB.view = "log"
            show()
        end
    end)
    toast:SetScript("OnEnter", function(self)
        if GameTooltip then
            GameTooltip:SetOwner(self, "ANCHOR_BOTTOM")
            GameTooltip:SetText(L("ui.open_log"), 1, 1, 1)
            GameTooltip:Show()
        end
    end)
    toast:SetScript("OnLeave", function()
        if GameTooltip then
            GameTooltip:Hide()
        end
    end)
    showSagaToast = function(titleText, questID)
        toastTitle:SetText(titleText or "Saga")
        toast.questID = questID
        toast.age = 0
        toast:SetAlpha(0)
        toast:Show()
        if PlaySoundFile then
            pcall(PlaySoundFile, "Interface\\AddOns\\Sagalore\\complete.mp3", "Master")
        end
    end
    built = true
end

SLASH_SAGALORE1 = "/sagalore"
SLASH_SAGALORE2 = "/sl"
SlashCmdList.SAGALORE = function(msg)
    msg = string.lower(string.gsub(msg or "", "^%s+", ""))
    if msg == "hide" then
        hide()
        return
    end
    if msg == "wipe" or msg == "clear" or msg == "reset" then
        -- Clear in-memory state and queue an API wipe. Companion deletes
        -- progress after /reload (SavedVariables flush). Editing the SV file
        -- on disk while logged in is overwritten on the next /reload.
        initDB()
        SagaloreDB.completions = {}
        SagaloreDB.objectives = {}
        markAllUnread()
        queueReset(nil)
        selectedID = nil
        chat("|cffc9a227Sagalore|r: " .. L("ui.wipe"))
        onWorld()
        if frame and frame:IsShown() then
            refresh()
        end
        return
    end
    if msg == "unread" then
        markAllUnread()
        chat("|cffc9a227Sagalore|r: " .. L("ui.unread_all"))
        updateMinimapUnread()
        if frame and frame:IsShown() then
            refresh()
        end
        return
    end
    local function resetQuest(id, label)
        initDB()
        SagaloreDB.completions[id] = nil
        if type(SagaloreDB.objectives) == "table" then
            SagaloreDB.objectives[id] = nil
        end
        local bucket = ensureReadMap()
        bucket[id] = nil
        queueReset(id)
        selectedID = id
        chat("|cffc9a227Sagalore|r: " .. string.format(L("ui.reset_one"), label))
        onWorld()
        show()
    end
    if msg == "reset selected" or msg == "reset this" then
        if type(selectedID) == "string" and selectedID ~= "" then
            local label = selectedID
            local entries = SagaloreData and SagaloreData.entries
            if type(entries) == "table" then
                for _, e in ipairs(entries) do
                    if type(e) == "table" and e.saga_id == selectedID and type(e.title) == "string" then
                        label = e.title
                        break
                    end
                end
            end
            resetQuest(selectedID, label)
            return
        end
        chat("|cffc9a227Sagalore|r: " .. L("ui.none_selected"))
        return
    end
    show()
end

-- Most slash emotes raise CHAT_MSG_TEXT_EMOTE. Silent ones (/sit, /stand,
-- and a few others) only animate — no chat line — so capture must come from
-- DoEmote and/or a SlashCmdList wrap. Keep the last DoEmote token and also
-- infer common tokens from the localized chat line as a fallback.
local lastDoEmoteToken = nil

local function inferEmoteToken(msg)
    local s = plainString(msg)
    if not s then
        return nil
    end
    s = string.lower(s)
    -- English + Norwegian fragments for the emotes we author in sagas.
    -- Prefer longer phrases first (e.g. "hearty hello" before generic words).
    -- Do not map bare "greet" → HELLO: /greet is GREET ("…warmly"), /hello is HELLO.
    local hints = {
        { "salute", "SALUTE" }, { "hils", "SALUTE" },
        { "bow", "BOW" }, { "bukk", "BOW" },
        { "wave", "WAVE" }, { "vink", "WAVE" },
        { "cheer", "CHEER" }, { "juble", "CHEER" },
        { "thank", "THANK" }, { "takk", "THANK" },
        { "applaud", "APPLAUD" }, { "applaus", "APPLAUD" }, { "bravo", "APPLAUD" },
        { "clap", "APPLAUD" }, { "klapp", "APPLAUD" },
        { "kneel", "KNEEL" }, { "knele", "KNEEL" },
        { "rude", "RUDE" }, { "frekk", "RUDE" }, { "ufin", "RUDE" },
        { "flex", "FLEX" },
        { "dance", "DANCE" }, { "dans", "DANCE" },
        { "hearty hello", "HELLO" }, { "hello", "HELLO" }, { "hei", "HELLO" },
        { "greetings", "GREET" }, { "warmly", "GREET" },
        { "slap", "SLAP" }, { "klask", "SLAP" },
        { "point", "POINT" }, { "pek", "POINT" },
        -- /talk is TALK. Specific phrases so a different emote is not stolen.
        { "you talk to", "TALK" }, { "you talk.", "TALK" }, { "you talk!", "TALK" },
        { "du snakker med", "TALK" }, { "du snakker.", "TALK" },
        { "du prater med", "TALK" }, { "du prater.", "TALK" },
    }
    for i = 1, #hints do
        if string.find(s, hints[i][1], 1, true) then
            return hints[i][2]
        end
    end
    return nil
end

local function noteEmote(token, unit, msg)
    local emote = plainString(token)
    if not emote or emote == "" then
        return
    end
    emote = string.upper(emote)
    if string.sub(emote, 1, 1) == "/" then
        emote = string.sub(emote, 2)
    end
    emote = (emote:gsub("%s+", "_"))
    -- /clap and /applaud are different tokens; authors usually write APPLAUD.
    if emote == "CLAP" then
        emote = "APPLAUD"
    end
    if emote == "" then
        return
    end
    lastDoEmoteToken = emote
    readLocation()
    local unitID = plainString(unit)
    local target = ""
    if unitID and unitID ~= "" and unitID ~= "none" and unitID ~= "player" and UnitName then
        local ok, n = pcall(UnitName, unitID)
        target = (ok and plainString(n)) or ""
    end
    if target == "" and UnitName then
        local ok, n = pcall(UnitName, "target")
        target = (ok and plainString(n)) or ""
    end
    if target == "" and world.object and world.object ~= "" then
        target = world.object
    end
    -- Do not wipe a good target when CHAT_MSG_TEXT_EMOTE re-enters without one.
    if target == "" and type(world.pendingEmote) == "table" then
        local prev = plainString(world.pendingEmote.target)
        if prev then
            target = prev
        end
    end
    world.pendingEmote = {
        token = emote,
        target = target,
        text = plainString(msg) or "",
        zone = world.zone,
        subzone = world.subzone,
        minimap = world.minimap,
        mapID = world.mapID,
        x = world.x,
        y = world.y,
        untilAt = unixNow() + 5,
    }
end

-- Unit name for a gossip-less interact. Players are not talk targets.
local function npcName(unit)
    if type(unit) ~= "string" or unit == "" or unit == "none" or unit == "player" or not UnitName then
        return nil
    end
    if UnitIsPlayer then
        local ok, isPlayer = pcall(UnitIsPlayer, unit)
        if ok and isPlayer == true and not secret(isPlayer) then
            return nil
        end
    end
    local ok, name = pcall(UnitName, unit)
    return ok and plainString(name) or nil
end

local function noteInteractUnit(unit)
    local name = npcName(unit)
    if not name then
        name = npcName("mouseover")
    end
    if not name then
        name = npcName("target")
    end
    noteTalkName(name)
end

-- Right-click / the interact key calls InteractUnit. Classic does not emit a
-- gossip event when the NPC has no dialogue, so latch the unit the same way
-- an open panel does. /talk remains the fallback when this hook does not run.
local interactHooked = false
local function installInteractHook()
    if interactHooked or type(hooksecurefunc) ~= "function" then
        return
    end
    local ok = pcall(function()
        hooksecurefunc("InteractUnit", function(unit)
            noteInteractUnit(unit)
            onWorld()
        end)
    end)
    if ok then
        interactHooked = true
    end
end

local emoteHooked = false
local silentSlashWrapped = false

-- /sit /stand (and a few sleeps) produce no CHAT_MSG_TEXT_EMOTE. Some Classic
-- builds also skip DoEmote for those slashes and call SitOrStand instead.
-- Wrap the slash handlers so authored SIT/STAND steps still fire.
local function installSilentEmoteSlashWrap()
    if silentSlashWrapped or type(SlashCmdList) ~= "table" then
        return
    end
    local map = {
        SIT = "SIT",
        STAND = "STAND",
        SLEEP = "SLEEP",
        LAYDOWN = "LAYDOWN",
        LAY = "LAYDOWN",
        LIE = "LAYDOWN",
        LIEDOWN = "LAYDOWN",
    }
    for cmd, token in pairs(map) do
        local prev = SlashCmdList[cmd]
        if type(prev) == "function" then
            SlashCmdList[cmd] = function(msg)
                noteEmote(token, "none")
                onWorld()
                return prev(msg)
            end
        end
    end
    silentSlashWrapped = true
end

local function installEmoteHook()
    if type(hooksecurefunc) == "function" and not emoteHooked then
        local ok = pcall(function()
            hooksecurefunc("DoEmote", function(token, unit)
                noteEmote(token, unit)
                onWorld()
            end)
        end)
        if ok then
            emoteHooked = true
        end
    end
    installSilentEmoteSlashWrap()
end

local inviteFrame
local shownInvite
local showInviteDialog
local maybeInvite
local shownWaitingInvite

local function emitInvite(action, id)
    if type(SagaloreInvite) ~= "table" or type(SagaloreInvite.line) ~= "function" then
        return
    end
    local line = SagaloreInvite.line(action, id)
    if line then
        chat(line)
    end
    initDB()
    if type(SagaloreDB.pending_invites) ~= "table" then
        SagaloreDB.pending_invites = {}
    end
    table.insert(SagaloreDB.pending_invites, { action = action, id = id, at = unixNow() })
end

local function finishInviteDialog(f)
    f:Hide()
    shownInvite = nil
    maybeInvite()
end

showInviteDialog = function(prompt)
    if not prompt or type(prompt.id) ~= "string" then
        return
    end
    if not inviteFrame then
        -- Must not use global name SagaloreInvite — that is the invite module table.
        local backdropTemplate = BackdropTemplateMixin and "BackdropTemplate" or nil
        local f = CreateFrame("Frame", "SagaloreInviteDialog", UIParent, backdropTemplate)
        f:SetSize(360, 92)
        f:SetPoint("CENTER")
        f:SetFrameStrata("DIALOG")
        f:EnableMouse(true)
        useBackdrop(f, {
            bgFile = "Interface\\DialogFrame\\UI-DialogBox-Background",
            edgeFile = "Interface\\DialogFrame\\UI-DialogBox-Border",
            tile = true, tileSize = 32, edgeSize = 32,
            insets = { left = 11, right = 12, top = 12, bottom = 11 },
        })
        f.text = f:CreateFontString(nil, "OVERLAY", "GameFontHighlight")
        f.text:SetPoint("TOP", 0, -16)
        f.text:SetWidth(320)
        f.text:SetJustifyH("CENTER")
        local function button(label, x)
            local b = CreateFrame("Button", nil, f, "UIPanelButtonTemplate")
            b:SetSize(100, 22)
            b:SetPoint("BOTTOM", x, 14)
            b:SetText(label)
            return b
        end
        f.accept = button("", -110)
        f.decline = button("", 0)
        f.later = button("", 110)
        f.accept:SetScript("OnClick", function()
            if f.inviteID then
                local groupName = f.inviteGroup or ""
                emitInvite("accept", f.inviteID)
                if type(SagaloreInvite) == "table" then
                    initDB()
                    if type(SagaloreInvite.markAccepted) == "function" then
                        SagaloreInvite.markAccepted(SagaloreDB, f.inviteID)
                    end
                    if type(SagaloreInvite.markAnswered) == "function" then
                        SagaloreInvite.markAnswered(SagaloreDB, f.inviteID)
                    end
                end
                if groupName ~= "" and DEFAULT_CHAT_FRAME then
                    DEFAULT_CHAT_FRAME:AddMessage("|cffc9a227Sagalore:|r " .. string.format(L("invite.accepted"), groupName))
                end
                refresh()
            end
            finishInviteDialog(f)
        end)
        f.decline:SetScript("OnClick", function()
            if f.inviteID then
                emitInvite("decline", f.inviteID)
                if type(SagaloreInvite) == "table" and type(SagaloreInvite.markAnswered) == "function" then
                    initDB()
                    SagaloreInvite.markAnswered(SagaloreDB, f.inviteID)
                end
            end
            finishInviteDialog(f)
        end)
        f.later:SetScript("OnClick", function()
            if f.inviteID and type(SagaloreInvite) == "table" then
                initDB()
                SagaloreInvite.snooze(SagaloreDB, f.inviteID, unixNow())
            end
            finishInviteDialog(f)
        end)
        inviteFrame = f
    end
    local text
    if prompt.key == "invite.include" then
        text = string.format(L("invite.include"), prompt.character or "", prompt.group or "")
    else
        text = string.format(L("invite.join"), prompt.group or "")
    end
    inviteFrame.text:SetText(text)
    local textH = inviteFrame.text:GetStringHeight() or 16
    inviteFrame:SetHeight(math.max(84, math.floor(textH + 52)))
    inviteFrame.accept:SetText(L("invite.accept"))
    inviteFrame.decline:SetText(L("invite.decline"))
    inviteFrame.later:SetText(L("invite.later"))
    inviteFrame.inviteID = prompt.id
    inviteFrame.inviteGroup = prompt.group or ""
    shownInvite = prompt.id
    inviteFrame:Show()
end

maybeInvite = function()
    if type(SagaloreInvite) ~= "table" or InCombatLockdown and InCombatLockdown() then
        return
    end
    initDB()
    if type(SagaloreInvite.reconcileAnswered) == "function" then
        SagaloreInvite.reconcileAnswered(SagaloreDB, SagaloreData)
    end
    local prompt = SagaloreInvite.prompt(SagaloreDB, player, SagaloreData, unixNow())
    if prompt then
        if shownInvite == prompt.id and inviteFrame and inviteFrame:IsShown() then
            return
        end
        showInviteDialog(prompt)
        return
    end
    if type(SagaloreInvite.waitingFor) ~= "function" then
        return
    end
    local waiting = SagaloreInvite.waitingFor(SagaloreDB, player, SagaloreData, unixNow())
    if not waiting or waiting.id == shownWaitingInvite then
        return
    end
    shownWaitingInvite = waiting.id
    if waiting.group and waiting.group ~= "" then
        chat(string.format(L("invite.wrong_character"), waiting.name or "", waiting.realm or "", waiting.group))
    else
        chat(string.format(L("invite.wrong_character_nogroup"), waiting.name or "", waiting.realm or ""))
    end
end

local events = CreateFrame("Frame")
local tick = 0
events:RegisterEvent("ADDON_LOADED")
events:RegisterEvent("PLAYER_LOGIN")
events:RegisterEvent("PLAYER_ENTERING_WORLD")
events:RegisterEvent("PLAYER_LEVEL_UP")
events:RegisterEvent("CHAT_MSG_ADDON")
events:RegisterEvent("CHAT_MSG_TEXT_EMOTE")
events:RegisterEvent("ZONE_CHANGED")
events:RegisterEvent("ZONE_CHANGED_INDOORS")
events:RegisterEvent("ZONE_CHANGED_NEW_AREA")
events:RegisterEvent("GOSSIP_SHOW")
events:RegisterEvent("GOSSIP_CLOSED")
events:RegisterEvent("QUEST_DETAIL")
events:RegisterEvent("QUEST_PROGRESS")
events:RegisterEvent("QUEST_COMPLETE")
events:RegisterEvent("QUEST_FINISHED")
events:RegisterEvent("MERCHANT_SHOW")
events:RegisterEvent("MERCHANT_CLOSED")
events:RegisterEvent("TRAINER_SHOW")
events:RegisterEvent("TRAINER_CLOSED")
events:RegisterEvent("PLAYER_TARGET_CHANGED")
events:RegisterEvent("BAG_UPDATE")
events:RegisterEvent("GET_ITEM_INFO_RECEIVED")
events:RegisterEvent("COMBAT_LOG_EVENT_UNFILTERED")
events:SetScript("OnUpdate", function(_, elapsed)
    if SagaloreBoard.pendingRefresh then
        SagaloreBoard.pendingRefresh = false
        if refresh then
            refresh()
        end
    end
    tick = tick + (elapsed or 0)
    if tick < 1 then
        return
    end
    tick = 0
    onWorld()
end)
events:SetScript("OnEvent", function(_, event, ...)
    if event == "ADDON_LOADED" then
        local name = ...
        if name ~= "Sagalore" then
            return
        end
        initDB()
        registerPrefix(PREFIX)
        installEmoteHook()
        installInteractHook()
        return
    end
    if event == "PLAYER_LOGIN" or event == "PLAYER_ENTERING_WORLD" then
        initDB()
        installEmoteHook()
        installInteractHook()
        showFreshNotice()
        cachePlayer()
        build()
        emitCharacter()
        onWorld()
        maybeInvite()
        return
    end
    if event == "PLAYER_LEVEL_UP" then
        local newLevel = ...
        local n = plainNumber(newLevel)
        if not n then
            local ok, converted = pcall(tonumber, newLevel)
            if ok then
                n = plainNumber(converted)
            end
        end
        if n and n > 0 then
            player.level = n
        else
            cachePlayer()
        end
        emitCharacter()
        onWorld()
        return
    end
    if event == "GOSSIP_SHOW" or event == "QUEST_DETAIL" or event == "QUEST_PROGRESS"
        or event == "QUEST_COMPLETE" or event == "MERCHANT_SHOW" or event == "TRAINER_SHOW" then
        readNPC()
        onWorld()
        return
    end
    if event == "GOSSIP_CLOSED" or event == "QUEST_FINISHED"
        or event == "MERCHANT_CLOSED" or event == "TRAINER_CLOSED" then
        clearNPC()
        return
    end
    if event == "PLAYER_TARGET_CHANGED" then
        readObject()
        -- Right-click on a gossip-less NPC only retargets. Require the cursor
        -- to still be on that unit so a camera-button tab-target does not count.
        if IsMouseButtonDown then
            local downOK, down = pcall(IsMouseButtonDown, "RightButton")
            if downOK and down then
                local hovered = npcName("mouseover")
                if same(hovered, npcName("target")) then
                    noteTalkName(hovered)
                end
            end
        end
        onWorld()
        return
    end
    if event == "GET_ITEM_INFO_RECEIVED" then
        if frame and frame:IsShown() then
            refresh()
        end
        return
    end
    if event == "BAG_UPDATE" or event == "ZONE_CHANGED" or event == "ZONE_CHANGED_INDOORS" or event == "ZONE_CHANGED_NEW_AREA" then
        onWorld()
        return
    end
    if event == "COMBAT_LOG_EVENT_UNFILTERED" then
        -- Critters often never fire PARTY_KILL; count UNIT_DIED after we hit them.
        if CombatLogGetCurrentEventInfo then
            local ok, _, subevent, _, sourceGUID, _, _, _, destGUID, destName = pcall(CombatLogGetCurrentEventInfo)
            if ok and type(subevent) == "string" then
                local me = UnitGUID and UnitGUID("player")
                if sourceGUID and me and sourceGUID == me and destGUID and (
                    subevent == "SWING_DAMAGE" or subevent == "SPELL_DAMAGE" or subevent == "RANGE_DAMAGE"
                    or subevent == "SPELL_PERIODIC_DAMAGE" or subevent == "DAMAGE_SHIELD"
                ) then
                    world.lastHitGUID = destGUID
                    world.lastHitName = plainString(destName)
                end
                -- Critters may fire both PARTY_KILL and UNIT_DIED; count each GUID once.
                local recentUntil = tonumber(world.recentKillUntil) or 0
                if world.recentKillGUID and unixNow() > recentUntil then
                    world.recentKillGUID = nil
                    world.recentKillUntil = nil
                end
                local already = destGUID and world.recentKillGUID == destGUID and unixNow() <= recentUntil
                local counted = false
                if subevent == "PARTY_KILL" and destGUID and not already then
                    counted = true
                elseif subevent == "UNIT_DIED" and destGUID and not already and destGUID == world.lastHitGUID then
                    counted = true
                    if (not destName or destName == "") and world.lastHitName then
                        destName = world.lastHitName
                    end
                end
                if counted and destGUID then
                    world.recentKillGUID = destGUID
                    world.recentKillUntil = unixNow() + 3
                    world.lastHitGUID = nil
                end
                local witnessed = counted
                    and destName and not secret(destName) and plainString(destName)
                if witnessed then
                    world.deathName = witnessed
                end
                if counted then
                    if secret(destName) then
                        world.killSecret = true
                        world.killName = nil
                    else
                        world.killSecret = false
                        world.killName = plainString(destName)
                    end
                    if witnessed then
                        witnessGroupKills(witnessed)
                    end
                    onWorld()
                    world.killName = nil
                    world.killSecret = false
                    world.deathName = nil
                end
            end
        end
        return
    end
    if event == "CHAT_MSG_TEXT_EMOTE" then
        -- Prefer the chat line (correct for this emote) over lastDoEmoteToken,
        -- which can be stale after a wrong-emote negative test (/wave then /salute).
        local msg = ...
        local inferred = inferEmoteToken(msg)
        local token = inferred
        if (not token or token == "") and type(world.pendingEmote) == "table" then
            token = world.pendingEmote.token
        end
        if (not token or token == "") then
            token = lastDoEmoteToken
        end
        if token and token ~= "" then
            noteEmote(token, "target", msg)
            lastDoEmoteToken = nil
            onWorld()
        elseif type(world.pendingEmote) == "table" then
            world.pendingEmote.untilAt = unixNow() + 5
            onWorld()
        end
        return
    end
    if event == "CHAT_MSG_ADDON" then
        local prefix, message, _, sender = ...
        if prefix ~= PREFIX or type(sender) ~= "string" then
            return
        end
        local msg = plainString(message)
        local shortName = sender
        if Ambiguate then
            local ok, value = pcall(Ambiguate, sender, "short")
            if ok then
                local s = plainString(value)
                if s then
                    shortName = s
                end
            end
        end
        if msg and string.sub(msg, 1, 2) == "H\t" then
            if player.name ~= "" and same(shortName, player.name) then
                return
            end
            local qid, oid
            local first = string.find(msg, "\t", 3, true)
            local second = first and string.find(msg, "\t", first + 1, true)
            if first and second then
                qid = string.sub(msg, 3, first - 1)
                oid = string.sub(msg, first + 1, second - 1)
            elseif first then
                qid = string.sub(msg, 3, first - 1)
                oid = string.sub(msg, first + 1)
            end
            local group = readGroup()
            local known = false
            for _, m in ipairs(group.members or {}) do
                if type(m) == "table" and same(m.name, shortName) then
                    known = true
                    break
                end
            end
            if known and qid and oid and oid ~= "" then
                rememberAttest(qid, oid, shortName, unixNow())
                onWorld()
            end
            return
        end
        if msg and string.sub(msg, 1, 2) == "G\t" then
            local qid
            local tabAt = string.find(msg, "\t", 3, true)
            if tabAt then
                qid = string.sub(msg, 3, tabAt - 1)
            end
            if player.name ~= "" and same(shortName, player.name) then
                return
            end
            local entry
            for _, e in ipairs(currentEntries()) do
                if e.saga_id == qid then
                    entry = e
                    break
                end
            end
            local group = readGroup()
            local known = false
            for _, m in ipairs(group.members or {}) do
                if type(m) == "table" and same(m.name, shortName) then
                    known = true
                    break
                end
            end
            if entry and not isComplete(entry) and known and type(entry.group) == "table"
                and SagaloreObjectives and SagaloreObjectives.groupOK and SagaloreObjectives.groupOK(entry.group, group) then
                finishQuest(entry, group)
            end
            return
        end
        local short = sender
        if Ambiguate then
            local ok, value = pcall(Ambiguate, sender, "short")
            if ok then
                local s = plainString(value)
                if s then
                    short = s
                end
            end
        end
        if player.name ~= "" and same(short, player.name) then
            return
        end
        chat("|cffc9a227Sagalore|r: " .. string.format(L("ui.completed_line"), short))
    end
end)
