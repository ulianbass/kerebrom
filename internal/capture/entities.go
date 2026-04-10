// Package capture extracts entities and relation candidates from text.
package capture

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Capitalized words that match proper names (Spanish + English).
var properNameRe = regexp.MustCompile(`\b[A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+(?:\s+[A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+)*\b`)

// Common words to filter out (not real entities).
var commonWords = map[string]bool{
	"the": true, "this": true, "that": true, "these": true, "those": true,
	"and": true, "but": true, "for": true, "with": true, "from": true,
	"not": true, "are": true, "was": true, "were": true, "been": true,
	"has": true, "have": true, "had": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"can": true, "shall": true, "must": true, "might": true,
	"yes": true, "also": true, "just": true, "then": true, "now": true,
	"here": true, "there": true, "when": true, "where": true, "how": true,
	"what": true, "which": true, "who": true, "whom": true, "why": true,
	"all": true, "each": true, "every": true, "both": true, "few": true,
	"more": true, "most": true, "some": true, "any": true, "many": true,
	"much": true, "other": true, "another": true, "such": true,
	"very": true, "own": true, "same": true, "than": true,
	// Spanish
	"los": true, "las": true, "del": true, "una": true, "uno": true,
	"por": true, "con": true, "sin": true, "para": true, "como": true,
	"pero": true, "mas": true, "que": true, "esto": true, "esta": true,
	"ese": true, "esa": true, "todo": true, "toda": true, "todos": true,
	"bien": true, "mal": true, "hay": true, "tiene": true, "hace": true,
	"solo": true, "donde": true, "cuando": true, "porque": true,
	"tambien": true, "siempre": true, "nunca": true, "ahora": true,
	"nombre": true, "legal": true, "pais": true, "idioma": true,
	"actividad": true, "principal": true, "broker": true, "cuenta": true,
	"proyecto": true, "actual": true, "anterior": true, "dominio": true,
	"objetivo": true, "herramientas": true, "resumen": true, "estado": true,
	"resultado": true, "total": true, "nuevo": true, "viejo": true,
	"repo": true, "version": true, "nota": true, "contexto": true,
	// WWWL format + common words that slip through
	"learned": true, "date": true,
	"personal": true, "long": true, "only": true, "once": true,
	"prefiero": true, "tengo": true, "trabajo": true,
	"binario": true, "motor": true, "regla": true, "meta": true,
	"ya": true, "uso": true, "estrategia": true,
	"disciplina": true, "fase": true, "pipeline": true, "fundamento": true,
	"fondos": true, "fuente": true, "premium": true, "live": true,
	"marca": true, "publica": true, "handoff": true, "completado": true,
	"aprobada": true, "descartadas": true, "rechazados": true,
	// Tech
	"function": true, "return": true, "import": true, "export": true,
	"class": true, "interface": true, "struct": true, "type": true,
	"true": true, "false": true, "null": true, "none": true,
	"error": true, "warning": true, "info": true, "debug": true,
}

// ExtractEntities finds proper names in text.
func ExtractEntities(text string) []string {
	matches := properNameRe.FindAllString(text, -1)
	seen := map[string]bool{}
	var result []string
	for _, m := range matches {
		canonical := CanonicalizeEntity(m)
		if len(canonical) < 2 || commonWords[canonical] {
			continue
		}
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		result = append(result, m)
	}
	return result
}

// CanonicalizeEntity lowercases and removes diacritics for dedup.
func CanonicalizeEntity(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, name)
	return result
}

// InferEntityType guesses the type based on heuristics.
// techWords are terms that should NEVER be classified as person.
var techWords = map[string]bool{
	"go": true, "python": true, "rust": true, "java": true, "api": true,
	"sdk": true, "cli": true, "sql": true, "http": true, "mcp": true,
	"json": true, "html": true, "css": true, "git": true, "npm": true,
	"docker": true, "linux": true, "mac": true, "windows": true,
	"trading": true, "strategy": true, "backtest": true, "profit": true,
	"loss": true, "sharpe": true, "drawdown": true, "equity": true,
	"bitcoin": true, "btc": true, "eth": true, "crypto": true,
	"bollinger": true, "macd": true, "rsi": true, "ema": true, "sma": true,
	"fibonacci": true, "donchian": true, "volume": true, "price": true,
	"code": true, "desktop": true, "server": true, "client": true,
	"dashboard": true, "database": true, "memory": true, "cache": true,
	"test": true, "debug": true, "deploy": true, "build": true,
	"config": true, "setup": true, "install": true, "update": true,
	"no": true, "si": true, "ok": true, "yes": true, "not": true,
	"new": true, "old": true, "top": true, "best": true, "next": true,
	"total": true, "result": true, "resumen": true, "repo": true,
	"railway": true, "cloudflare": true, "vercel": true, "heroku": true,
	"sqlite": true, "postgresql": true, "redis": true, "kafka": true,
	"es": true, "en": true, "de": true, "la": true, "el": true,
	// Project names and trading terms (not people)
	"falage": true, "quamtos": true, "kerebrom": true, "alejandria": true,
	"eois": true, "fre": true, "ibs": true, "orb": true,
	"databento": true, "wyckoff": true, "ict": true,
	"breakthrough": true, "estrategia": true,
}

func InferEntityType(name string) string {
	lower := strings.ToLower(name)

	// Single-word tech terms → always concept.
	if techWords[lower] {
		return "concept"
	}

	// Location indicators
	locations := []string{"city", "country", "state", "province", "town",
		"ciudad", "pais", "estado", "guatemala", "mexico", "colombia",
		"argentina", "españa", "peru", "chile", "ecuador", "bolivia",
		"usa", "canada", "london", "paris", "berlin", "tokyo"}
	for _, loc := range locations {
		if strings.Contains(lower, loc) {
			return "location"
		}
	}
	// Organization indicators
	orgs := []string{"inc", "corp", "ltd", "llc", "company", "foundation",
		"university", "institute", "google", "microsoft", "anthropic",
		"openai", "github", "apple", "amazon", "meta",
		"quantfury", "binance", "codex", "claude"}
	for _, org := range orgs {
		if strings.Contains(lower, org) {
			return "organization"
		}
	}
	// Multi-word with tech terms → concept.
	for _, w := range strings.Fields(lower) {
		if techWords[w] {
			return "concept"
		}
	}
	// Default: if starts with capital and is short, likely a person.
	// But only if ALL words look like proper names (no numbers, no special chars).
	words := strings.Fields(name)
	if len(words) <= 3 && len(words) >= 1 {
		allCapitalized := true
		for _, w := range words {
			if len(w) == 0 || !unicode.IsUpper(rune(w[0])) {
				allCapitalized = false
			}
		}
		if allCapitalized {
			return "person"
		}
	}
	return "concept"
}
