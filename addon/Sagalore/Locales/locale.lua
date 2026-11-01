-- Resolves a catalog key. Missing keys fall back to English.
-- Other catalogs (nb, …) stay loadable for later; the product ships English
-- only for now. An explicit SagaloreLanguage override is kept for tests.
local function catalog(lang)
    local all = SagaloreLocale or {}
    if lang == "nb" and type(all.nb) == "table" then
        return all.nb
    end
    return all.en or {}
end

local function clientLang()
    local set = SagaloreLanguage
    if set == "en" or set == "nb" then
        return set
    end
    return "en"
end

function Sagalore_L(key)
    if type(key) ~= "string" or key == "" then
        return ""
    end
    local lang = clientLang()
    local value = catalog(lang)[key]
    if value == nil and lang ~= "en" then
        local en = catalog("en")
        value = en[key]
    end
    if value == nil then
        return key
    end
    return value
end
