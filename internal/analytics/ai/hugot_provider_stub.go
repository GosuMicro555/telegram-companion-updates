//go:build ORT

package ai

import (
	"context"
	"errors"
)

var ErrORTUnavailable = errors.New("ORT provider is unavailable until Ubuntu and macOS artifacts pass verification")

type HugotProvider struct{}

func NewHugotProvider(context.Context, string, ModelMetadata) (*HugotProvider, error) {
	return nil, ErrORTUnavailable
}
func (*HugotProvider) Embed(context.Context, []string) ([][]float32, ModelMetadata, error) {
	return nil, ModelMetadata{}, ErrORTUnavailable
}
func (*HugotProvider) Close() error { return nil }

var _ EmbeddingProvider = (*HugotProvider)(nil)
