// Package embed provides text embedding for semantic search.
//
// Two implementations:
//   - HashEmbedder: zero-dependency, character n-gram hashing (default)
//   - ONNXEmbedder: real semantic embeddings via ONNX runtime (build tag "onnx")
package embed

import (
	"encoding/binary"
	"math"
)

// Embedder produces fixed-dimensional vectors from text.
type Embedder interface {
	Embed(text string) ([]float32, error)
	Dimensions() int
}

// AutoSelect returns the best available Embedder.
// With build tag "onnx": ONNXEmbedder (384-dim).
// Without: HashEmbedder (256-dim).
func AutoSelect() Embedder {
	return autoSelect()
}

// CosineSimilarity computes the cosine similarity between two L2-normalized vectors.
// Returns a value in [-1, 1]. Both vectors must have the same length.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot float32
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}

// NormalizeL2 normalizes a vector to unit length in place.
func NormalizeL2(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := float32(math.Sqrt(sum))
	if norm == 0 {
		return
	}
	for i := range v {
		v[i] /= norm
	}
}

// SerializeEmbedding converts float32 slice to BLOB for SQLite storage.
// Uses little-endian IEEE 754 (4 bytes per float32).
func SerializeEmbedding(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// DeserializeEmbedding converts BLOB back to float32 slice.
func DeserializeEmbedding(data []byte) []float32 {
	if len(data) == 0 || len(data)%4 != 0 {
		return nil
	}
	v := make([]float32, len(data)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return v
}
