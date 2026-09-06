package analytics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractCanonicalWordsGroupsRussianAndEnglishForms(t *testing.T) {
	words := ExtractCanonicalWords([]Document{
		{Text: "\u0414\u0435\u043d\u044c\u0433\u0438 \u043d\u0443\u0436\u043d\u044b \u0441\u0435\u0433\u043e\u0434\u043d\u044f", SourceID: "message-1"},
		{Text: "\u041d\u0435\u0442 \u0434\u0435\u043d\u0435\u0433, \u0434\u0443\u043c\u0430\u044e \u043e \u0434\u0435\u043d\u044c\u0433\u0430\u0445", SourceID: "message-2"},
		{Text: "buy buys bought buying run running", SourceID: "message-3"},
		{Text: "покупка покупку покупки", SourceID: "message-4"},
	})

	money := canonicalByValue(t, words, "\u0434\u0435\u043d\u044c\u0433\u0438")
	require.Equal(t, "ru", money.Language)
	require.Equal(t, 3, money.Frequency)
	require.Equal(t, 2, money.MessageCount)
	require.ElementsMatch(t, []string{"\u0434\u0435\u043d\u044c\u0433\u0438", "\u0434\u0435\u043d\u0435\u0433", "\u0434\u0435\u043d\u044c\u0433\u0430\u0445"}, formValues(money.Forms))

	buy := canonicalByValue(t, words, "buy")
	require.Equal(t, "en", buy.Language)
	require.Equal(t, 4, buy.Frequency)
	run := canonicalByValue(t, words, "run")
	require.Equal(t, 2, run.Frequency)
	purchase := canonicalByValue(t, words, "покупка")
	require.Equal(t, 3, purchase.Frequency)
	require.ElementsMatch(t, []string{"покупка", "покупку", "покупки"}, formValues(purchase.Forms))
}

func TestExtractCanonicalWordsGroupsConservativeMoneyAliasFamilies(t *testing.T) {
	words := ExtractCanonicalWords([]Document{
		{Text: "бабки бабосы бабосиков", SourceID: "message-1"},
		{Text: "деньги деньжата денюжки деньгосы", SourceID: "message-2"},
	})

	babki := canonicalByValue(t, words, "бабки")
	require.Equal(t, "ru", babki.Language)
	require.Equal(t, 3, babki.Frequency)
	require.ElementsMatch(t, []string{"бабки", "бабосы", "бабосиков"}, formValues(babki.Forms))

	money := canonicalByValue(t, words, "деньги")
	require.Equal(t, "ru", money.Language)
	require.Equal(t, 4, money.Frequency)
	require.ElementsMatch(t, []string{"деньги", "деньжата", "денюжки", "деньгосы"}, formValues(money.Forms))
}

func TestExtractCanonicalWordsExcludesNoiseAndPhrases(t *testing.T) {
	words := ExtractCanonicalWords([]Document{{
		Text:     "https://example.test @user 42 ! a I \u0438 \u0434\u0435\u043d\u044c\u0433\u0438 \u0441\u0435\u0433\u043e\u0434\u043d\u044f",
		SourceID: "message-1",
	}})

	require.NotEmpty(t, words)
	for _, word := range words {
		require.NotContains(t, word.Canonical, " ")
		require.NotEqual(t, "42", word.Canonical)
		require.NotEqual(t, "a", word.Canonical)
		require.NotEqual(t, "i", word.Canonical)
		require.NotEqual(t, "\u0438", word.Canonical)
	}
}

func TestCanonicalTokenSetCanonicalizesUniquePhraseTokens(t *testing.T) {
	require.Equal(t, map[string]struct{}{
		"\u043c\u0430\u0448\u0438\u043d\u0430": {},
		"\u0435\u0434\u0435\u0442":             {},
	}, CanonicalTokenSet([]string{"\u043c\u0430\u0448\u0438\u043d\u0430", "\u0435\u0434\u0435\u0442", "\u043c\u0430\u0448\u0438\u043d\u0430"}))
}

func canonicalByValue(t *testing.T, words []CanonicalWord, value string) CanonicalWord {
	t.Helper()
	for _, word := range words {
		if word.Canonical == value {
			return word
		}
	}
	t.Fatalf("canonical word %q not found in %#v", value, words)
	return CanonicalWord{}
}

func formValues(forms []CanonicalForm) []string {
	values := make([]string, len(forms))
	for index, form := range forms {
		values[index] = form.Value
	}
	return values
}
