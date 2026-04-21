package prompt

import (
	"strings"
	"unicode"
)

func IsSubstantive(content string) bool {
	text := strings.TrimSpace(strings.ToLower(content))
	text = strings.Trim(text, " \t\r\n.!?¡¿,;:")
	if text == "" {
		return false
	}
	if len([]rune(text)) <= 1 || isNumericOnly(text) {
		return false
	}
	casualOnly := map[string]bool{
		"adelante":       true,
		"aun":            true,
		"aún":            true,
		"continua":       true,
		"continúa":       true,
		"de una":         true,
		"go":             true,
		"gracias":        true,
		"hazlo":          true,
		"mira":           true,
		"ok":             true,
		"okay":           true,
		"listo":          true,
		"dale":           true,
		"no":             true,
		"bueno":          true,
		"muchas gracias": true,
		"perfecto":       true,
		"revisa":         true,
		"revisalo":       true,
		"revísalo":       true,
		"sigue":          true,
		"si":             true,
		"sí":             true,
		"thanks":         true,
		"thank you":      true,
		"vale":           true,
		"ya":             true,
	}
	return !casualOnly[text]
}

func isNumericOnly(text string) bool {
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			continue
		}
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
