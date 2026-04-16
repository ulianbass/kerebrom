package prompt

import "testing"

func TestIsSubstantive(t *testing.T) {
	tests := map[string]bool{
		"Gracias.":                             false,
		"ok":                                   false,
		"Sí":                                   false,
		"Bueno":                                false,
		"Perfecto!":                            false,
		"Hazlo":                                true,
		"Borra eso":                            true,
		"Close Engram parity for Kerebrom v1.": true,
		"Un gracias es un prompt?":             true,
		"Guarda este prompt aunque llegue como camelCase.": true,
	}

	for content, want := range tests {
		if got := IsSubstantive(content); got != want {
			t.Fatalf("IsSubstantive(%q) = %v, want %v", content, got, want)
		}
	}
}
