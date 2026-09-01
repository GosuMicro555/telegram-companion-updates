package analytics

import (
	"sort"
	"strings"
	"time"
	"unicode"

	analytictext "telegram-companion/internal/analytics/text"
)

type Language string

const (
	LanguageRU Language = "ru"
	LanguageEN Language = "en"
)

type KeywordClass string

const (
	ClassNeutral  KeywordClass = "neutral"
	ClassPositive KeywordClass = "positive"
	ClassNegative KeywordClass = "negative"
	ClassService  KeywordClass = "service"
)

type FormObservation struct {
	Form        string
	Frequency   int64
	MessageKeys []string
	LastSeen    time.Time
}

type CanonicalObservation struct {
	Canonical      string
	Language       Language
	TotalFrequency int64
	MessageCount   int64
	LastSeen       time.Time
	Forms          []FormObservation
}

type CanonicalImportValue struct {
	Value    string
	Language Language
	Forms    []string
}

type CanonicalBulkImportResult struct {
	Added   int
	Updated int
	Skipped int
}

type CanonicalKeyword struct {
	ID             string
	Canonical      string
	Language       Language
	Class          KeywordClass
	DecisionSource string
	TriggerActive  bool
	TotalFrequency int64
	FrequencyDelta int64
	MessageCount   int64
	LastSeen       time.Time
	Forms          []CanonicalKeywordForm
}

type CanonicalKeywordForm struct {
	Value          string
	DisplayValue   string
	Frequency      int64
	ManualOverride bool
}

type CanonicalMetrics struct {
	KeywordCount  int64
	PositiveCount int64
	NegativeCount int64
	ServiceCount  int64
	TriggerCount  int64
	FormCount     int64
	LogicalBytes  int64
}

type CanonicalForm struct {
	Value     string
	Frequency int
}

type CanonicalWord struct {
	Canonical    string
	Language     string
	Class        string
	Frequency    int
	MessageCount int
	Forms        []CanonicalForm
}

type canonicalAggregate struct {
	word     CanonicalWord
	forms    map[string]int
	messages map[string]struct{}
}

var canonicalExceptions = map[string]string{
	"\u0434\u0435\u043d\u044c\u0433\u0438":                         "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u0435\u0433":                               "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044c\u0433\u0430\u043c":                   "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044c\u0433\u0430\u043c\u0438":             "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044c\u0433\u0430\u0445":                   "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044c\u0436\u0430\u0442\u0430":             "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044c\u0436\u0430\u0442":                   "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044c\u0436\u0430\u0442\u0430\u043c":       "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044c\u0436\u0430\u0442\u0430\u043c\u0438": "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044e\u0436\u043a\u0438":                   "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044e\u0436\u0435\u043a":                   "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044e\u0436\u043a\u0430\u043c":             "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044e\u0436\u043a\u0430\u043c\u0438":       "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044c\u0433\u043e\u0441\u044b":             "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044c\u0433\u043e\u0441\u043e\u0432":       "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044c\u0433\u043e\u0441\u0430\u043c":       "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0434\u0435\u043d\u044c\u0433\u043e\u0441\u0430\u043c\u0438": "\u0434\u0435\u043d\u044c\u0433\u0438",
	"\u0431\u0430\u0431\u043a\u0438":                               "\u0431\u0430\u0431\u043a\u0438",
	"\u0431\u0430\u0431\u043e\u043a":                               "\u0431\u0430\u0431\u043a\u0438",
	"\u0431\u0430\u0431\u043a\u0430\u043c":                         "\u0431\u0430\u0431\u043a\u0438",
	"\u0431\u0430\u0431\u043a\u0430\u043c\u0438":                   "\u0431\u0430\u0431\u043a\u0438",
	"\u0431\u0430\u0431\u043e\u0441\u044b":                         "\u0431\u0430\u0431\u043a\u0438",
	"\u0431\u0430\u0431\u043e\u0441\u043e\u0432":                   "\u0431\u0430\u0431\u043a\u0438",
	"\u0431\u0430\u0431\u043e\u0441\u0430\u043c":                   "\u0431\u0430\u0431\u043a\u0438",
	"\u0431\u0430\u0431\u043e\u0441\u0430\u043c\u0438":             "\u0431\u0430\u0431\u043a\u0438",
	"\u0431\u0430\u0431\u043e\u0441\u0438\u043a\u0438":             "\u0431\u0430\u0431\u043a\u0438",
	"\u0431\u0430\u0431\u043e\u0441\u0438\u043a\u043e\u0432":       "\u0431\u0430\u0431\u043a\u0438",
	"buy":    "buy",
	"buys":   "buy",
	"buying": "buy",
	"bought": "buy",
}

func ExtractCanonicalWords(documents []Document) []CanonicalWord {
	aggregates := make(map[string]*canonicalAggregate)
	for documentIndex, document := range documents {
		messageID := strings.TrimSpace(document.SourceID)
		if messageID == "" {
			messageID = stableSource(documentIndex)
		}
		for _, sentence := range analytictext.Sentences(document.Text) {
			for _, token := range sentence {
				language := tokenLanguage(token)
				if language == "" || tokenNoise(token) {
					continue
				}
				canonical := canonicalToken(token, language)
				key := language + "\x00" + canonicalGroupKey(token, language)
				item := aggregates[key]
				if item == nil {
					item = &canonicalAggregate{
						word:  CanonicalWord{Canonical: canonical, Language: language, Class: "neutral"},
						forms: make(map[string]int), messages: make(map[string]struct{}),
					}
					aggregates[key] = item
				}
				item.word.Frequency++
				item.forms[token]++
				item.messages[messageID] = struct{}{}
			}
		}
	}
	result := make([]CanonicalWord, 0, len(aggregates))
	for _, item := range aggregates {
		item.word.MessageCount = len(item.messages)
		for value, frequency := range item.forms {
			item.word.Forms = append(item.word.Forms, CanonicalForm{Value: value, Frequency: frequency})
		}
		sort.Slice(item.word.Forms, func(i, j int) bool {
			if item.word.Forms[i].Frequency != item.word.Forms[j].Frequency {
				return item.word.Forms[i].Frequency > item.word.Forms[j].Frequency
			}
			return item.word.Forms[i].Value < item.word.Forms[j].Value
		})
		item.word.Canonical = preferredCanonical(item.word.Forms)
		result = append(result, item.word)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Frequency != result[j].Frequency {
			return result[i].Frequency > result[j].Frequency
		}
		return result[i].Canonical < result[j].Canonical
	})
	return result
}

// CanonicalTokenSet returns the unique canonical tokens used for trigger matching.
func CanonicalTokenSet(tokens []string) map[string]struct{} {
	result := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		language := tokenLanguage(token)
		if language == "" {
			continue
		}
		result[canonicalToken(token, language)] = struct{}{}
	}
	return result
}

func canonicalGroupKey(token, language string) string {
	if canonical, ok := canonicalExceptions[token]; ok {
		return canonical
	}
	if language == "ru" {
		return analytictext.RussianStemmer{}.Stem(token)
	}
	return canonicalToken(token, language)
}

func preferredCanonical(forms []CanonicalForm) string {
	for _, form := range forms {
		if canonical, ok := canonicalExceptions[form.Value]; ok {
			return canonical
		}
	}
	if len(forms) == 0 {
		return ""
	}
	best := forms[0]
	for _, form := range forms[1:] {
		if form.Frequency > best.Frequency ||
			(form.Frequency == best.Frequency && (len([]rune(form.Value)) < len([]rune(best.Value)) ||
				(len([]rune(form.Value)) == len([]rune(best.Value)) && form.Value < best.Value))) {
			best = form
		}
	}
	return best.Value
}

func canonicalToken(token, language string) string {
	if canonical, ok := canonicalExceptions[token]; ok {
		return canonical
	}
	if language == "en" {
		runes := []rune(token)
		for _, suffix := range []string{"ing", "ied", "ed", "es", "s"} {
			if strings.HasSuffix(token, suffix) && len(runes) > len([]rune(suffix))+2 {
				base := strings.TrimSuffix(token, suffix)
				if suffix == "ied" {
					return base + "y"
				}
				if suffix == "ing" || suffix == "ed" {
					baseRunes := []rune(base)
					if len(baseRunes) > 2 && baseRunes[len(baseRunes)-1] == baseRunes[len(baseRunes)-2] {
						base = string(baseRunes[:len(baseRunes)-1])
					}
				}
				return base
			}
		}
	}
	return token
}

func tokenLanguage(token string) string {
	language := ""
	for _, r := range token {
		current := ""
		switch {
		case unicode.In(r, unicode.Cyrillic):
			current = "ru"
		case unicode.In(r, unicode.Latin):
			current = "en"
		default:
			return ""
		}
		if language != "" && language != current {
			return ""
		}
		language = current
	}
	return language
}

func tokenNoise(token string) bool {
	runes := []rune(token)
	if len(runes) <= 1 {
		return true
	}
	for _, r := range runes {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
