local src = debug.getinfo(1, "S").source
local dir = src:match("^@(.*)[/\\][^/\\]+$") or "."
local M = dofile(dir .. "/Sagalore/SagaloreChains.lua")

local function assertTrue(cond, msg)
    if not cond then
        error(msg or "assertion failed", 2)
    end
end

local entries = {
    { saga_id = "a", title = "En kveld", chain_id = "goldshire", chain_name = "Goldshire", chain_order = 1, chain_index = 1, chain_total = 4 },
    {
        saga_id = "b", title = "Veien", chain_id = "goldshire", chain_name = "Goldshire", chain_order = 2, chain_index = 2, chain_total = 4,
        requires = { { id = "a", title = "En kveld" } },
    },
    {
        saga_id = "c", title = "Melk", chain_id = "goldshire", chain_name = "Goldshire", chain_order = 3, chain_index = 3, chain_total = 4,
        requires = { "a", "b" },
    },
    { saga_id = "d", title = "Siste", chain_id = "goldshire", chain_name = "Goldshire", chain_order = 4 },
}

assertTrue(M.chapterLabel(entries[2]) == "Chapter 2 of 4", "server index")
assertTrue(M.chapterLabel({ saga_id = "b", chain_id = "goldshire", chain_order = 2 }, entries) == "Chapter 2 of 4", "local rank")
assertTrue(M.lockText(M.missing(entries[2], entries, {})) == "Locked: finish En kveld first", "one prereq")
assertTrue(M.isLocked(entries[2], entries, {}), "locked before")
assertTrue(not M.isLocked(entries[2], entries, { a = true }), "any completion unlocks, version ignored")
assertTrue(M.lockText(M.missing(entries[3], entries, {})) == "Locked: finish En kveld and Veien first", "two titles")
assertTrue(M.listSub(entries[2], entries, {}, "Ukjent") == "Chapter 2 of 4 — Locked: finish En kveld first", "list lock")
assertTrue(M.listSub(entries[2], entries, { a = true }, "Ukjent") == "Goldshire · Chapter 2 of 4", "list chapter")

local unlocked = M.newlyUnlocked(entries, {}, { a = true })
assertTrue(#unlocked == 1 and unlocked[1].saga_id == "b", "local unlock of the next chapter")
assertTrue(not M.isLocked(entries[1], entries, { a = true }), "finished saga is not locked")

local detail = M.detailLines(entries[2], entries, {})
assertTrue(detail:find("Chapter 2 of 4", 1, true) ~= nil, "detail chapter")
assertTrue(detail:find("Locked: finish En kveld first", 1, true) ~= nil, "detail lock")

print("chains ok")
