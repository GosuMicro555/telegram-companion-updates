//go:build !ORT

package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

const (
	pinnedModelSize     = int64(470268510)
	pinnedTokenizerSize = int64(17082730)
)

type HugotProvider struct {
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
	metadata ModelMetadata
}

func NewHugotProvider(ctx context.Context, modelDir string, metadata ModelMetadata) (*HugotProvider, error) {
	if metadata != PinnedModelMetadata() {
		return nil, errors.New("hugot provider metadata must match the pinned model manifest")
	}
	for _, artifact := range []struct {
		name   string
		size   int64
		digest string
	}{
		{"model.onnx", pinnedModelSize, metadata.ModelSHA256},
		{"tokenizer.json", pinnedTokenizerSize, metadata.TokenizerSHA256},
	} {
		if err := verifyArtifact(filepath.Join(modelDir, artifact.name), artifact.size, artifact.digest); err != nil {
			return nil, fmt.Errorf("verify %s: %w", artifact.name, err)
		}
	}
	session, err := hugot.NewGoSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("create Hugot Go session: %w", err)
	}
	pipeline, err := hugot.NewPipeline(session, hugot.FeatureExtractionConfig{
		ModelPath: modelDir, Name: "multilingual-e5-small", OnnxFilename: "model.onnx",
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create Hugot feature extraction pipeline: %w", err), session.Destroy())
	}
	return &HugotProvider{session: session, pipeline: pipeline, metadata: metadata}, nil
}

func (p *HugotProvider) Embed(ctx context.Context, texts []string) ([][]float32, ModelMetadata, error) {
	if p == nil || p.pipeline == nil {
		return nil, ModelMetadata{}, errors.New("hugot provider is not initialized")
	}
	if len(texts) == 0 {
		return nil, ModelMetadata{}, errors.New("embedding input is empty")
	}
	output, err := p.pipeline.RunPipeline(ctx, texts)
	if err != nil {
		return nil, ModelMetadata{}, fmt.Errorf("run Hugot feature extraction: %w", err)
	}
	return output.Embeddings, p.metadata, nil
}

func (p *HugotProvider) Close() error {
	if p == nil || p.session == nil {
		return nil
	}
	return p.session.Destroy()
}

func verifyArtifact(path string, expectedSize int64, expectedDigest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("artifact is not a regular file")
	}
	if info.Size() != expectedSize {
		return fmt.Errorf("size is %d, expected %d", info.Size(), expectedSize)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != expectedDigest {
		return fmt.Errorf("SHA-256 is %s, expected %s", actual, expectedDigest)
	}
	return nil
}

var _ EmbeddingProvider = (*HugotProvider)(nil)
