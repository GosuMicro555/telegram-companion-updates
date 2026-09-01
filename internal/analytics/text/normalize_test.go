package text

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeRussianUnicodeAndNoise(t *testing.T) {
	require.Equal(t, "елка не дешевая", Normalize("  ЁЛКА — не дешёвая! https://example.test @seller "))
}

func TestSentencesAndTokensDoNotLeakLinksOrMentions(t *testing.T) {
	got := Sentences("Цена выросла! Напишите @seller. https://example.test Не хватает денег?")
	require.Equal(t, [][]string{{"цена", "выросла"}, {"напишите"}, {"не", "хватает", "денег"}}, got)
}

func TestRussianStemmerNormalizesInflectedSurfaceForms(t *testing.T) {
	stemmer := RussianStemmer{}
	require.Equal(t, stemmer.Stem("покупка"), stemmer.Stem("покупку"))
}
