//go:build onnx

package embed

// autoSelect returns ONNXEmbedder when the onnx build tag is set.
// Falls back to HashEmbedder if ONNX initialization fails.
func autoSelect() Embedder {
	e, err := NewONNXEmbedder()
	if err != nil {
		return NewHashEmbedder(256)
	}
	return e
}
