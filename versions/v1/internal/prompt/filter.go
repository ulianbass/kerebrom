package prompt

import (
	"strings"
)

func IsSubstantive(content string) bool {
	text := strings.TrimSpace(strings.ToLower(content))
	text = strings.Trim(text, " \t\r\n.!?¡¿,;:")
	if len([]rune(text)) < 10 {
		return false
	}
	casualOnly := map[string]bool{
		"gracias":        true,
		"muchas gracias": true,
		"thanks":         true,
		"thank you":      true,
		"ok":             true,
		"okay":           true,
		"listo":          true,
		"dale":           true,
		"perfecto":       true,
	}
	return !casualOnly[text]
}
