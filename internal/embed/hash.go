package embed

import (
	"crypto/sha256"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// HashEmbedder produces embeddings via character n-gram feature hashing.
// Zero dependencies. Deterministic. Not semantic — but good enough for
// exact/near-exact matching and as a fallback when ONNX is unavailable.
type HashEmbedder struct {
	dims int
}

// NewHashEmbedder creates a HashEmbedder with the given dimensions (default 256).
func NewHashEmbedder(dims int) *HashEmbedder {
	if dims <= 0 {
		dims = 256
	}
	return &HashEmbedder{dims: dims}
}

func (h *HashEmbedder) Dimensions() int { return h.dims }

func (h *HashEmbedder) Embed(text string) ([]float32, error) {
	text = strings.ToLower(removeDiacriticsEmbed(text))
	tokens := tokenizeEmbed(text)
	vec := make([]float32, h.dims)

	for _, token := range tokens {
		// Token-level hash (weight 2.0).
		bucket := hashToBucket(token, h.dims)
		vec[bucket] += 2.0

		// Character n-grams (3-5), FastText-style with padding.
		padded := "<" + token + ">"
		for n := 3; n <= 5; n++ {
			for i := 0; i+n <= len(padded); i++ {
				ngram := padded[i : i+n]
				bucket := hashToBucket(ngram, h.dims)
				vec[bucket] += 1.0
			}
		}
	}

	NormalizeL2(vec)
	return vec, nil
}

// hashToBucket maps a string to a bucket index [0, dims) using SHA-256.
func hashToBucket(s string, dims int) int {
	h := sha256.Sum256([]byte(s))
	// Use first 4 bytes as uint32.
	val := int(h[0])<<24 | int(h[1])<<16 | int(h[2])<<8 | int(h[3])
	if val < 0 {
		val = -val
	}
	return val % dims
}

func removeDiacriticsEmbed(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, s)
	return result
}

func tokenizeEmbed(text string) []string {
	var tokens []string
	var current strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			if current.Len() >= 2 {
				tokens = append(tokens, current.String())
			}
			current.Reset()
		}
	}
	if current.Len() >= 2 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
