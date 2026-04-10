//go:build onnx

package embed

import "fmt"

// ONNXEmbedder uses the ONNX runtime to produce 384-dim embeddings
// from the all-MiniLM-L6-v2 model. Requires libonnxruntime installed.
//
// TODO(phase2): Implement with yalue/onnxruntime_go + clems4ever/all-minilm-l6-v2-go.
// For now this is a placeholder that will be filled when we add the ONNX dep.
type ONNXEmbedder struct {
	dims int
}

func NewONNXEmbedder() (*ONNXEmbedder, error) {
	return nil, fmt.Errorf("ONNX embedder not yet implemented — use hash fallback")
}

func (o *ONNXEmbedder) Embed(text string) ([]float32, error) {
	return nil, fmt.Errorf("ONNX not initialized")
}

func (o *ONNXEmbedder) Dimensions() int { return 384 }
