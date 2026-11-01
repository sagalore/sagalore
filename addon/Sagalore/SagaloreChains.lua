-- Saga chains. No WoW API calls, so lua5.1 can test this file.
-- A prerequisite counts when its saga id is completed locally or in the
-- synced log. The version it was completed on does not matter.
-- requires items are saga ids or { id, title }.

local M = {}

local function L(key)
    if type(Sagalore_L) == "function" then
        local value = Sagalore_L(key)
        if type(value) == "string" and value ~= "" and value ~= key then
            return value
        end
    end
    return nil
end

local function requireID(item)
    if type(item) == "string" then
        if item == "" then
            return nil, nil
        end
        return item, nil
    end
    if type(item) == "table" then
        local id = item.id
        if type(id) ~= "string" or id == "" then
            return nil, nil
        end
        local title = item.title
        if type(title) ~= "string" or title == "" then
            title = nil
        end
        return id, title
    end
    return nil, nil
end

local function titleOf(id, hinted, entries)
    if type(hinted) == "string" and hinted ~= "" then
        return hinted
    end
    if type(entries) == "table" then
        for i = 1, #entries do
            local entry = entries[i]
            if type(entry) == "table" and entry.saga_id == id and type(entry.title) == "string" and entry.title ~= "" then
                return entry.title
            end
        end
    end
    return id
end

function M.isDone(id, entries, done)
    if type(id) ~= "string" or id == "" then
        return false
    end
    if type(done) == "table" and done[id] then
        return true
    end
    if type(entries) == "table" then
        for i = 1, #entries do
            local entry = entries[i]
            if type(entry) == "table" and entry.saga_id == id and entry.status == "completed" then
                return true
            end
        end
    end
    return false
end

function M.missing(entry, entries, done)
    local out = {}
    if type(entry) ~= "table" or type(entry.requires) ~= "table" then
        return out
    end
    for i = 1, #entry.requires do
        local id, hinted = requireID(entry.requires[i])
        if id and not M.isDone(id, entries, done) then
            out[#out + 1] = { id = id, title = titleOf(id, hinted, entries) }
        end
    end
    return out
end

function M.isLocked(entry, entries, done)
    if type(entry) ~= "table" then
        return false
    end
    if M.isDone(entry.saga_id, entries, done) then
        return false
    end
    return #M.missing(entry, entries, done) > 0
end

local function joinNames(names)
    if #names == 1 then
        return names[1]
    end
    local conj = L("chain.and") or "and"
    if #names == 2 then
        return names[1] .. " " .. conj .. " " .. names[2]
    end
    local head = {}
    for i = 1, #names - 1 do
        head[i] = names[i]
    end
    return table.concat(head, ", ") .. " " .. conj .. " " .. names[#names]
end

function M.lockText(missing)
    if type(missing) ~= "table" or #missing == 0 then
        return nil
    end
    local names = {}
    for i = 1, #missing do
        local title = missing[i].title
        if type(title) ~= "string" or title == "" then
            title = missing[i].id or (L("chain.a_saga") or "a saga")
        end
        names[i] = title
    end
    local pattern = L("chain.locked") or "Locked: finish %s first"
    return string.format(pattern, joinNames(names))
end

function M.chapterLabel(entry, entries)
    if type(entry) ~= "table" then
        return nil
    end
    local index = tonumber(entry.chain_index)
    local total = tonumber(entry.chain_total)
    local chainID = entry.chain_id
    if (not index or index < 1 or not total or total < 1) and type(chainID) == "string" and chainID ~= "" and type(entries) == "table" then
        local group = {}
        for i = 1, #entries do
            local other = entries[i]
            if type(other) == "table" and other.chain_id == chainID then
                group[#group + 1] = other
            end
        end
        table.sort(group, function(a, b)
            local ao = tonumber(a.chain_order) or 0
            local bo = tonumber(b.chain_order) or 0
            if ao ~= bo then
                return ao < bo
            end
            return (a.saga_id or "") < (b.saga_id or "")
        end)
        total = #group
        for i = 1, #group do
            if group[i].saga_id == entry.saga_id then
                index = i
                break
            end
        end
    end
    if not index or not total or index < 1 or total < 1 then
        return nil
    end
    return string.format(L("chain.chapter") or "Chapter %d of %d", index, total)
end

function M.listSub(entry, entries, done, fallback)
    local chapter = M.chapterLabel(entry, entries)
    if M.isLocked(entry, entries, done) then
        local lock = M.lockText(M.missing(entry, entries, done))
        if chapter and lock then
            return chapter .. " — " .. lock
        end
        return lock or fallback or ""
    end
    if chapter then
        if type(entry.chain_name) == "string" and entry.chain_name ~= "" then
            return entry.chain_name .. " · " .. chapter
        end
        return chapter
    end
    return fallback or ""
end

function M.detailLines(entry, entries, done)
    local lines = {}
    if type(entry) == "table" and type(entry.chain_name) == "string" and entry.chain_name ~= "" then
        lines[#lines + 1] = entry.chain_name
    end
    local chapter = M.chapterLabel(entry, entries)
    if chapter then
        lines[#lines + 1] = chapter
    end
    if type(entry) == "table" and not M.isDone(entry.saga_id, entries, done) then
        local lock = M.lockText(M.missing(entry, entries, done))
        if lock then
            lines[#lines + 1] = lock
        end
    end
    return table.concat(lines, "\n")
end

function M.newlyUnlocked(entries, before, after)
    local out = {}
    if type(entries) ~= "table" then
        return out
    end
    for i = 1, #entries do
        local entry = entries[i]
        if type(entry) == "table" and not M.isDone(entry.saga_id, entries, before) then
            if M.isLocked(entry, entries, before) and not M.isLocked(entry, entries, after) then
                out[#out + 1] = entry
            end
        end
    end
    table.sort(out, function(a, b)
        local ao = tonumber(a.chain_order) or 0
        local bo = tonumber(b.chain_order) or 0
        if ao ~= bo then
            return ao < bo
        end
        return (a.title or "") < (b.title or "")
    end)
    return out
end

SagaloreChains = M
return M
