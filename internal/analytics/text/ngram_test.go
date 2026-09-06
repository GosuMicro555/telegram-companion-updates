package text

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNGramsKeepMeaningfulStopwordsInsidePhrase(t *testing.T) {
	got := Generate("Мне не хватает денег на покупку", 1, 4)
	require.Contains(t, got, "не хватает денег")
	require.Contains(t, got, "не хватает денег на")
	require.NotContains(t, got, "мне не хватает денег на")
}

func TestGenerateDoesNotCrossSentenceBoundaries(t *testing.T) {
	got := Generate("Нет денег. Нужна покупка", 1, 4)
	require.NotContains(t, got, "денег нужна")
	require.Contains(t, got, "нет денег")
}
