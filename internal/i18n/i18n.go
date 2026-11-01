// Package i18n is the companion catalog.
// English is the fallback. API error codes stay English in the API package.
package i18n

import "strings"

const (
	EN = "en"
	NB = "nb"
)

// Normalize accepts en, nb, and a few aliases. Anything else is English.
func Normalize(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "nb", "no", "nb-no", "nb_no", "norwegian", "norsk":
		return NB
	default:
		return EN
	}
}

// T returns the catalog string. A missing key in nb uses English.
// A key missing from English is returned unchanged.
func T(lang, key string) string {
	lang = Normalize(lang)
	if lang == NB {
		if v, ok := nb[key]; ok {
			return v
		}
	}
	if v, ok := en[key]; ok {
		return v
	}
	return key
}

// Catalog returns a copy of one language, with English filled in for gaps.
func Catalog(lang string) map[string]string {
	lang = Normalize(lang)
	out := make(map[string]string, len(en))
	for k, v := range en {
		out[k] = v
	}
	if lang == NB {
		for k, v := range nb {
			out[k] = v
		}
	}
	return out
}

// NBKeys returns the Norwegian keys. Tests require each one to exist in English.
func NBKeys() map[string]string {
	out := make(map[string]string, len(nb))
	for k, v := range nb {
		out[k] = v
	}
	return out
}

func ENKeys() map[string]string {
	out := make(map[string]string, len(en))
	for k, v := range en {
		out[k] = v
	}
	return out
}

var en = map[string]string{
	"app.logged_out":   "Not logged in",
	"app.account":      "Account: %s",
	"app.login":        "Log in with Battle.net",
	"app.logout":       "Log out",
	"app.login_tip":    "Open the browser and log in",
	"app.logout_tip":   "Log out of Sagalore on this computer",
	"app.sync":         "Sync now",
	"app.sync_tip":     "Fetch sagas from the API",
	"app.quit":         "Quit",
	"app.quit_tip":     "Quit Sagalore",
	"app.language":     "Language: %s",
	"app.language_en":  "English",
	"app.language_nb":  "Norwegian",
	"app.version":      "Version",
	"app.update_ready": "New version %s — restart",
	"app.auto_update":  "Automatic updates",
	"app.running":      "Sagalore is running. Log in with Battle.net if asked. Quit with Ctrl+C.",
	"app.synced":       "Synced. Type /reload in the game if you are logged in.",
	"app.api_required": "--api-url or SAGALORE_API_URL is required",

	"news.created":         "New saga: %s — type /reload in WoW",
	"news.created_addon":   "New saga: %s is loaded.",
	"news.updated":         "Saga updated: %s — type /reload in WoW",
	"news.updated_addon":   "Saga updated: %s is loaded.",
	"news.retired":         "Saga retired: %s — type /reload in WoW",
	"news.retired_addon":   "Saga retired: %s is loaded.",
	"news.completed":       "Saga completed: %s — type /reload in WoW",
	"news.completed_addon": "Saga completed: %s is loaded.",
	"news.extra":           " and %d more",

	"setup.wow":      "WoW folder (empty = search, _retail_ when the client is retail)",
	"setup.client":   "Client: forever, retail, classic_era, or classic",
	"setup.repo":     "GitHub repo for updates (owner/name)",
	"setup.token":    "GitHub token (empty if the repo is public)",
	"setup.auto":     "Automatic updates (yes/no)",
	"setup.language": "Language (en or nb)",
}

var nb = map[string]string{
	"app.logged_out":   "Ikke innlogget",
	"app.account":      "Konto: %s",
	"app.login":        "Logg inn med Battle.net",
	"app.logout":       "Logg ut",
	"app.login_tip":    "Åpne nettleseren og logg inn",
	"app.logout_tip":   "Logg ut av Sagalore på denne maskinen",
	"app.sync":         "Synkroniser nå",
	"app.sync_tip":     "Hent sagaer fra API-et",
	"app.quit":         "Avslutt",
	"app.quit_tip":     "Avslutt Sagalore",
	"app.language":     "Språk: %s",
	"app.language_en":  "Engelsk",
	"app.language_nb":  "Norsk",
	"app.version":      "Versjon",
	"app.auto_update":  "Automatiske oppdateringer",
	"app.running":      "Sagalore kjører. Logg inn med Battle.net om du blir bedt om det. Avslutt med Ctrl+C.",
	"app.synced":       "Synkronisert. Skriv /reload i spillet hvis du er innlogget.",
	"app.api_required": "--api-url eller SAGALORE_API_URL må være satt",

	"news.created":         "Ny saga: %s — skriv /reload i WoW",
	"news.created_addon":   "Ny saga: %s er lastet inn.",
	"news.updated":         "Saga oppdatert: %s — skriv /reload i WoW",
	"news.updated_addon":   "Saga oppdatert: %s er lastet inn.",
	"news.retired":         "Saga avsluttet: %s — skriv /reload i WoW",
	"news.retired_addon":   "Saga avsluttet: %s er lastet inn.",
	"news.completed":       "Saga fullført: %s — skriv /reload i WoW",
	"news.completed_addon": "Saga fullført: %s er lastet inn.",
	"news.extra":           " og %d til",

	"setup.wow":      "Mappe til WoW (tom = let automatisk, _retail_ når klient er retail)",
	"setup.client":   "Klient: forever, retail, classic_era eller classic",
	"setup.repo":     "GitHub-repo for oppdateringer (eier/navn)",
	"setup.token":    "GitHub-token (tom hvis repoet er offentlig)",
	"setup.auto":     "Automatiske oppdateringer (ja/nei)",
	"setup.language": "Språk (en eller nb)",
}
