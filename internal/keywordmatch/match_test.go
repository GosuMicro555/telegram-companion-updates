package keywordmatch

import "testing"

func TestMatchNormalizesRussianAndEnglishWords(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		text       string
		want       bool
	}{
		{name: "case insensitive", expression: "ДЕНЬГИ", text: "Мне нужны деньги", want: true},
		{name: "yo normalized", expression: "елка", text: "Нарядили ёлку", want: true},
		{name: "russian word form", expression: "машина", text: "У машины тихий мотор", want: true},
		{name: "russian irregular form", expression: "деньги", text: "Совсем нет денег", want: true},
		{name: "english word form", expression: "buy", text: "She is buying a car", want: true},
		{name: "english irregular form", expression: "buy", text: "They bought a car", want: true},
		{name: "word boundary", expression: "кот", text: "Это скотина", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Match(tt.expression, tt.text); got != tt.want {
				t.Fatalf("Match(%q, %q) = %v, want %v", tt.expression, tt.text, got, tt.want)
			}
		})
	}
}

func TestBangFixesWordForm(t *testing.T) {
	if !Match("!машина", "МАШИНА приехала") {
		t.Fatal("exact word form should still be case-insensitive")
	}
	if Match("!машина", "У машины тихий мотор") {
		t.Fatal("! must reject another grammatical form")
	}
}

func TestBangMakesServiceWordRequired(t *testing.T) {
	if Match("!в москве", "Москва сегодня") {
		t.Fatal("! must make an exact service word significant")
	}
	if !Match("!в москве", "Сегодня в Москве") {
		t.Fatal("an exact service word should match when it is present")
	}
	if Match(`"!в москве"`, "Москва") {
		t.Fatal("an exact service word must remain significant inside quotes")
	}
}

func TestPlusMakesServiceWordRequired(t *testing.T) {
	if !Match("как купить", "Купить машину") {
		t.Fatal("an unforced service word should be ignored")
	}
	if Match("+как купить", "Купить машину") {
		t.Fatal("+ must make the service word required")
	}
	if !Match("+как купить", "Как лучше купить машину") {
		t.Fatal("the forced service word is present")
	}
}

func TestCommonRussianAndEnglishPronounsAreServiceWords(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		text       string
	}{
		{name: "russian personal pronoun", expression: "я купить", text: "Купить машину"},
		{name: "russian declined pronoun", expression: "мне купить", text: "Купить машину"},
		{name: "russian plural pronoun", expression: "они купить", text: "Купить машину"},
		{name: "english first person pronoun", expression: "i buy", text: "Buy a car"},
		{name: "english third person pronoun", expression: "she buy", text: "Buy a car"},
		{name: "english plural pronoun", expression: "they buy", text: "Buy a car"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !Match(tt.expression, tt.text) {
				t.Fatalf("unforced service word in %q should be ignored", tt.expression)
			}
		})
	}
}

func TestUnquotedTermsMustOccurInOneSentence(t *testing.T) {
	if !Match("машина едет", "Сейчас машина очень тихо едет домой") {
		t.Fatal("unordered terms in one sentence should match")
	}
	if Match("машина едет", "Машина стоит. Поезд едет.") {
		t.Fatal("terms from different sentences must not be combined")
	}
}

func TestQuotesFixSignificantWordCountButNotOrder(t *testing.T) {
	if !Match(`"купить машину"`, "Машину купить") {
		t.Fatal("quotes should allow a different word order")
	}
	if !Match(`"купить машину"`, "Купить и машину") {
		t.Fatal("unforced service words should not change quoted word count")
	}
	if Match(`"купить машину"`, "Хочу купить новую машину") {
		t.Fatal("quotes must reject additional significant words")
	}
}

func TestBracketsFixOrderAndKeepTheSequenceAdjacent(t *testing.T) {
	if !Match(`[купить машину]`, "Хочу купить машину сегодня") {
		t.Fatal("brackets should allow additional words outside the fixed sequence")
	}
	if Match(`[купить машину]`, "Машину хочу купить сегодня") {
		t.Fatal("brackets must reject reversed order")
	}
	if Match(`[купить машину]`, "Хочу купить новую машину сегодня") {
		t.Fatal("brackets must reject additional words inside the fixed sequence")
	}
}

func TestBracketsKeepServiceWordsAndRejectInsertedWords(t *testing.T) {
	expression := `[из москвы в париж]`
	if !Match(expression, "Билеты на самолет из Москвы в Париж") {
		t.Fatal("the complete fixed sequence should match inside a longer sentence")
	}
	if Match(expression, "Москва Париж") {
		t.Fatal("service words inside brackets must be required")
	}
	if Match(expression, "Из Москвы недорого в Париж") {
		t.Fatal("brackets must reject a word inserted into the fixed sequence")
	}
}

func TestParenthesesAndPipeProvideAlternatives(t *testing.T) {
	if !Match("(купить|заказать) машину", "Можно заказать новую машину") {
		t.Fatal("second alternative should match")
	}
	if Match("(купить|заказать) машину", "Можно арендовать машину") {
		t.Fatal("a value outside the alternatives must not match")
	}
}

func TestMinusExcludesWordOrPhrase(t *testing.T) {
	if Match("купить машину -дешево", "Хочу дешево купить машину") {
		t.Fatal("minus word must suppress a positive match")
	}
	if !Match("купить машину -дешево", "Хочу купить новую машину") {
		t.Fatal("positive terms without the excluded word should match")
	}
	if Match(`купить машину -[после аварии]`, "Хочу купить машину после аварии") {
		t.Fatal("an excluded ordered phrase must suppress a match")
	}
}

func TestOperatorsCanBeCombined(t *testing.T) {
	expression := `"(!купить|!заказать) +в [москве сегодня]"`
	if !Match(expression, "Заказать в Москве сегодня") {
		t.Fatal("combined expression should match")
	}
	if Match(expression, "Заказать Москве сегодня") {
		t.Fatal("forced service word must remain required inside quotes")
	}
	if Match(expression, "Заказать в сегодня Москве") {
		t.Fatal("bracket order must remain fixed inside quotes")
	}
	if Match(expression, "Хочу заказать в Москве сегодня") {
		t.Fatal("quotes must still reject an additional significant word")
	}
}

func TestMatchesAny(t *testing.T) {
	if !MatchesAny([]string{"деньги -кредит", "(машина|авто) едет"}, "Авто быстро едет") {
		t.Fatal("MatchesAny should return true for any matching expression")
	}
	if MatchesAny([]string{"", "деньги -кредит"}, "Кредит приносит деньги") {
		t.Fatal("MatchesAny should ignore empty expressions and honor exclusions")
	}
}

func TestMatchesAnyExclusionTreatsStandaloneMinusEntryAsGlobalExclusion(t *testing.T) {
	if !MatchesAnyExclusion([]string{"-деньги"}, "У меня совсем нет денег") {
		t.Fatal("a standalone minus entry should match the excluded word in a global exclusion list")
	}
	if MatchesAnyExclusion([]string{"-деньги"}, "Мне одобрили кредит") {
		t.Fatal("a standalone minus entry must not match text without the excluded word")
	}
	if MatchesAny([]string{"-деньги"}, "У меня совсем нет денег") {
		t.Fatal("a standalone exclusion must not become a positive keyword")
	}
}

func TestMatchesAnyExclusionSupportsNegativeOnlyAlternatives(t *testing.T) {
	expressions := []string{"-spam|-scam"}
	if !MatchesAnyExclusion(expressions, "This message contains scam") {
		t.Fatal("the second negative alternative should match a global exclusion")
	}
	if !MatchesAnyExclusion(expressions, "This message contains spam") {
		t.Fatal("the first negative alternative should match a global exclusion")
	}
	if MatchesAnyExclusion(expressions, "This message is harmless") {
		t.Fatal("negative alternatives must not match unrelated text")
	}
	if MatchesAny(expressions, "This message contains scam") {
		t.Fatal("negative-only alternatives must not become positive triggers")
	}
}

func TestInvalidExpressionsDoNotMatchOrPanic(t *testing.T) {
	for _, expression := range []string{"", `"деньги`, "[машина", "(купить|)", "-", "()", "[]"} {
		t.Run(expression, func(t *testing.T) {
			if Match(expression, "деньги купить машину") {
				t.Fatalf("invalid expression %q must not match", expression)
			}
		})
	}
}
