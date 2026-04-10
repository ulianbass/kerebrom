//go:build !onnx

package embed

// autoSelect returns HashEmbedder when ONNX build tag is not set.
func autoSelect() Embedder {
	return NewHashEmbedder(256)
}
