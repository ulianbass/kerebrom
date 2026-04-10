package capture

import "regexp"

// RelationCandidate is a potential subject-predicate-object triple.
type RelationCandidate struct {
	Predicate  string  // e.g. "name", "lives_in", "works_at"
	Object     string  // the entity name
	Confidence float64 // [0, 1]
	EntityType string  // person, location, organization, concept
}

type relationPattern struct {
	predicate  string
	re         *regexp.Regexp
	confidence float64
	entityType string
}

var relationPatterns = []relationPattern{
	// Name (first person + third person + WWWL format)
	{"name", regexp.MustCompile(`(?i)(?:me llamo|my name is|i'?m|soy)\s+([A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+(?:\s+[A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+)*)`), 0.95, "person"},
	{"name", regexp.MustCompile(`(?i)(?:nombre[:\s]+|name[:\s]+)([A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+(?:\s+[A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+)*)`), 0.90, "person"},
	// Location (first person + third person + WWWL)
	{"lives_in", regexp.MustCompile(`(?i)(?:vivo en|i live in|resido en)\s+([A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+(?:\s+[A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+)*)`), 0.92, "location"},
	{"lives_in", regexp.MustCompile(`(?i)(?:de |from )(Guatemala|Mexico|Colombia|Argentina|España|Peru|Chile|Ecuador|Bolivia)`), 0.90, "location"},
	{"from", regexp.MustCompile(`(?i)(?:soy de|i'?m from|vengo de)\s+([A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+(?:\s+[A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+)*)`), 0.85, "location"},
	// Work
	{"works_at", regexp.MustCompile(`(?i)(?:trabajo en|work (?:at|for)|i work at)\s+([A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+(?:\s+[A-ZÁÉÍÓÚÜÑ][a-záéíóúüñ]+)*)`), 0.90, "organization"},
	{"role", regexp.MustCompile(`(?i)(?:soy un|soy una|i'?m a|i am a|i am an)\s+(\w+(?:\s+\w+){0,2})`), 0.88, "concept"},
	// Preferences
	{"prefers", regexp.MustCompile(`(?i)(?:prefiero|i prefer|me gusta más)\s+(\w+(?:\s+\w+){0,3})`), 0.75, "concept"},
	{"likes", regexp.MustCompile(`(?i)(?:me gusta|i like|me encanta|i love)\s+(\w+(?:\s+\w+){0,3})`), 0.72, "concept"},
	{"dislikes", regexp.MustCompile(`(?i)(?:no me gusta|i (?:don'?t|do not) like|odio|i hate)\s+(\w+(?:\s+\w+){0,3})`), 0.72, "concept"},
	// Learning/using
	{"uses", regexp.MustCompile(`(?i)(?:uso|i use|utilizo|we use)\s+(\w+(?:\s+\w+){0,2})`), 0.76, "concept"},
	{"learning", regexp.MustCompile(`(?i)(?:estoy aprendiendo|i'?m learning|aprendiendo)\s+(\w+(?:\s+\w+){0,2})`), 0.78, "concept"},
	// Studies
	{"studied", regexp.MustCompile(`(?i)(?:estudié|i (?:studied|majored in))\s+(\w+(?:\s+\w+){0,3})`), 0.82, "concept"},
}

// ExtractRelationCandidates finds potential relations in text.
// The "subject" is always the user (implicit).
func ExtractRelationCandidates(text string) []RelationCandidate {
	var candidates []RelationCandidate
	for _, p := range relationPatterns {
		matches := p.re.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			obj := m[1]
			if len(obj) < 2 {
				continue
			}
			candidates = append(candidates, RelationCandidate{
				Predicate:  p.predicate,
				Object:     obj,
				Confidence: p.confidence,
				EntityType: p.entityType,
			})
		}
	}
	return candidates
}
