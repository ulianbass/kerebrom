package prompt

import (
	"strings"
)

func IsSubstantive(content string) bool {
	text := strings.TrimSpace(strings.ToLower(content))
	text = strings.Trim(text, " \t\r\n.!?¡¿,;:")
	if text == "" {
		return false
	}
	casualOnly := map[string]bool{
		"gracias":        true,
		"muchas gracias": true,
		"thanks":         true,
		"thank you":      true,
		"ok":             true,
		"okay":           true,
		"si":             true,
		"sí":             true,
		"no":             true,
		"bueno":          true,
		"vale":           true,
		"listo":          true,
		"dale":           true,
		"perfecto":       true,
	}
	return !casualOnly[text]
}
