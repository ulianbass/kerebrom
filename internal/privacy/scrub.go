package privacy

import (
	"regexp"
	"strings"
)

// SensitiveMatch records what was found and redacted.
type SensitiveMatch struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Compiled PII patterns.
var patterns = []struct {
	label string
	re    *regexp.Regexp
}{
	{"EMAIL", regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)},
	{"API_KEY", regexp.MustCompile(`\b(?:sk|rk|pk)_[A-Za-z0-9]{10,}\b`)},
	{"BEARER_TOKEN", regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._\-]{12,}\b`)},
	{"JWT", regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9._\-]+\.[A-Za-z0-9._\-]+\b`)},
	{"ASSIGNMENT_SECRET", regexp.MustCompile(`(?i)\b(?:api[_\-]?key|token|secret|password)\s*[:=]\s*([^\s,;]+)`)},
}

var privateTagRe = regexp.MustCompile(`(?s)<private>.*?</private>`)

// StripPrivateTags removes <private>...</private> blocks.
func StripPrivateTags(text string) string {
	return strings.TrimSpace(privateTagRe.ReplaceAllString(text, ""))
}

// ScrubSensitive detects and redacts PII patterns.
// Returns the scrubbed text and a list of matches found.
func ScrubSensitive(text string) (string, []SensitiveMatch) {
	text = StripPrivateTags(text)
	var matches []SensitiveMatch

	for _, p := range patterns {
		locs := p.re.FindAllString(text, -1)
		for _, loc := range locs {
			matches = append(matches, SensitiveMatch{Label: p.label, Value: loc})
			text = strings.ReplaceAll(text, loc, "[REDACTED:"+p.label+"]")
		}
	}
	return text, matches
}
