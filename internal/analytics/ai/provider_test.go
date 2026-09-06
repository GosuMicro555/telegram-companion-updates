//go:build !ORT

package ai

import (
	"context"
	"path/filepath"
	"testing"

	"telegram-companion/internal/domain"

	"github.com/stretchr/testify/require"
)

func TestEmbedLocalRejectsTelegramBeforeProvider(t *testing.T) {
	provider := &countingProvider{}

	_, _, err := EmbedLocal(context.Background(), provider, domain.SourceKindTelegram, []string{"message"})

	require.ErrorIs(t, err, ErrSourceKindNotAllowed)
	require.Zero(t, provider.calls)
}

func TestNewHugotProviderRequiresPinnedLocalArtifacts(t *testing.T) {
	_, err := NewHugotProvider(context.Background(), filepath.Join(t.TempDir(), "missing"), PinnedModelMetadata())
	require.Error(t, err)
	require.Contains(t, err.Error(), "model.onnx")
}

func TestPinnedModelMetadataIsImmutable(t *testing.T) {
	require.Equal(t, "intfloat/multilingual-e5-small", PinnedModelMetadata().Repository)
	require.Equal(t, "614241f622f53c4eeff9890bdc4f31cfecc418b3", PinnedModelMetadata().Revision)
	require.Equal(t, "ca456c06b3a9505ddfd9131408916dd79290368331e7d76bb621f1cba6bc8665", PinnedModelMetadata().ModelSHA256)
	require.Equal(t, "0b44a9d7b51c3c62626640cda0e2c2f70fdacdc25bbbd68038369d14ebdf4c39", PinnedModelMetadata().TokenizerSHA256)
}

func TestEmbedLocalAcceptsOnlyLocalImport(t *testing.T) {
	provider := &countingProvider{}

	vectors, metadata, err := EmbedLocal(context.Background(), provider, domain.SourceKindLocalImport, []string{"record"})

	require.NoError(t, err)
	require.Equal(t, [][]float32{{1, 0}}, vectors)
	require.Equal(t, "tiny", metadata.Name)
	require.Equal(t, 1, provider.calls)
}

type countingProvider struct{ calls int }

func (p *countingProvider) Embed(context.Context, []string) ([][]float32, ModelMetadata, error) {
	p.calls++
	return [][]float32{{1, 0}}, ModelMetadata{Name: "tiny"}, nil
}
