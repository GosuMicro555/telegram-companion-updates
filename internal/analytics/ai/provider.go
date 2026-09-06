package ai

import (
	"context"
	"errors"
	"fmt"

	"telegram-companion/internal/domain"
)

var ErrSourceKindNotAllowed = errors.New("AI embeddings require a rights-confirmed local import")

type ModelMetadata = domain.ModelMetadata

type EmbeddingProvider interface {
	Embed(ctx context.Context, texts []string) ([][]float32, ModelMetadata, error)
}

func PinnedModelMetadata() ModelMetadata {
	return ModelMetadata{
		Name: "multilingual-e5-small", Repository: "intfloat/multilingual-e5-small",
		Revision:        "614241f622f53c4eeff9890bdc4f31cfecc418b3",
		ModelSHA256:     "ca456c06b3a9505ddfd9131408916dd79290368331e7d76bb621f1cba6bc8665",
		TokenizerSHA256: "0b44a9d7b51c3c62626640cda0e2c2f70fdacdc25bbbd68038369d14ebdf4c39",
	}
}

func EmbedLocal(ctx context.Context, provider EmbeddingProvider, sourceKind domain.SourceKind, texts []string) ([][]float32, ModelMetadata, error) {
	if sourceKind != domain.SourceKindLocalImport {
		return nil, ModelMetadata{}, fmt.Errorf("%w: source_kind=%q", ErrSourceKindNotAllowed, sourceKind)
	}
	if provider == nil {
		return nil, ModelMetadata{}, errors.New("embedding provider is required")
	}
	return provider.Embed(ctx, texts)
}
