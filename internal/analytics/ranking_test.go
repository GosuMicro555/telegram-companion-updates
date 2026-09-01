package analytics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRankClustersExactAndSimHashDuplicatesDeterministically(t *testing.T) {
	docs := []Document{
		{Text: "Мне не хватает денег на покупку", SourceID: "chat-1"},
		{Text: "мне не хватает денег на покупку!", SourceID: "chat-2"},
		{Text: "Мне не хватает денег для покупки", SourceID: "chat-3"},
	}
	first := Rank(docs, nil)
	second := Rank(docs, nil)
	require.Equal(t, first, second)
	require.NotEmpty(t, first)
	require.Equal(t, "не хватает денег", first[0].DisplayValue)
	require.GreaterOrEqual(t, first[0].Frequency, 2)
	require.GreaterOrEqual(t, first[0].SourceDiversity, 2)
	require.Equal(t, first[0].Score, first[0].Components.Total())
}

func TestRankUsesNamedScoresAndStableTieBreaks(t *testing.T) {
	got := Rank([]Document{{Text: "бета альфа", SourceID: "one"}}, nil)
	require.NotEmpty(t, got)
	for _, candidate := range got {
		require.Equal(t, candidate.Score, candidate.Components.Total())
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Score == got[i].Score {
			require.LessOrEqual(t, got[i-1].NormalizedValue, got[i].NormalizedValue)
		}
	}
}

func TestSelectLimitsGeneralAndProfileResults(t *testing.T) {
	docs := make([]Document, 0, 140)
	for i := 0; i < 140; i++ {
		docs = append(docs, Document{Text: wordFor(i) + " не хватает денег", SourceID: wordFor(i)})
	}
	result := Select(Rank(docs, DefaultMoneyShortageProfile()))
	require.Len(t, result.GeneralPhrases, 100)
	require.LessOrEqual(t, len(result.GeneralWords), 30)
	require.LessOrEqual(t, len(result.ProfileCandidates), 100)
}

func wordFor(i int) string {
	return string([]rune{'т', rune('а' + i%32), rune('а' + (i/32)%32), rune('а' + (i/1024)%32)})
}
