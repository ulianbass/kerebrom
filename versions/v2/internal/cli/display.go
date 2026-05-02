package cli

import (
	"strings"
	"unicode"
)

var displayLabelOverrides = map[string]string{
	"Stop": "Session Stop",
}

func displayLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if label, ok := displayLabelOverrides[value]; ok {
		return label
	}

	words := splitIdentifierWords(value)
	for i, word := range words {
		words[i] = titleDisplayWord(word)
	}
	return strings.Join(words, " ")
}

func splitIdentifierWords(value string) []string {
	var words []string
	var current []rune
	previousLowerOrDigit := false

	flush := func() {
		if len(current) == 0 {
			return
		}
		words = append(words, string(current))
		current = nil
	}

	for _, r := range value {
		if r == '_' || r == '-' || r == '.' || r == '/' || unicode.IsSpace(r) {
			flush()
			previousLowerOrDigit = false
			continue
		}
		if len(current) > 0 && unicode.IsUpper(r) && previousLowerOrDigit {
			flush()
		}
		current = append(current, r)
		previousLowerOrDigit = unicode.IsLower(r) || unicode.IsDigit(r)
	}
	flush()

	return words
}

func titleDisplayWord(word string) string {
	if word == strings.ToUpper(word) {
		return word
	}
	lower := strings.ToLower(word)
	runes := []rune(lower)
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
