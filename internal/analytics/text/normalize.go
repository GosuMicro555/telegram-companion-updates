package text

import (
	"strings"
	"unicode"
)

// Stemmer is the deterministic morphology boundary used by the Telegram pipeline.
type Stemmer interface {
	Stem(string) string
}

type IdentityStemmer struct{}

func (IdentityStemmer) Stem(token string) string { return token }

// RussianStemmer is intentionally small and rule-based; it keeps analytics
// deterministic while providing a replaceable morphology boundary.
type RussianStemmer struct{}

func (RussianStemmer) Stem(token string) string {
	token = Normalize(token)
	runes := []rune(token)
	for _, suffix := range []string{"иями", "ами", "ями", "ого", "ему", "ому", "ую", "юю", "ах", "ях", "ка", "ку", "ки", "ы", "и", "а", "я", "у", "ю", "е", "о"} {
		suffixRunes := []rune(suffix)
		if len(runes) > len(suffixRunes)+2 && strings.HasSuffix(token, suffix) {
			return string(runes[:len(runes)-len(suffixRunes)])
		}
	}
	return token
}

func Normalize(value string) string {
	return strings.Join(tokens(value), " ")
}

func Sentences(value string) [][]string {
	value = stripNoise(value)
	var result [][]string
	var sentence strings.Builder
	flush := func() {
		if got := tokens(sentence.String()); len(got) > 0 {
			result = append(result, got)
		}
		sentence.Reset()
	}
	for _, r := range value {
		switch r {
		case '.', '!', '?', '\n', '\r':
			flush()
		default:
			sentence.WriteRune(r)
		}
	}
	flush()
	return result
}

func StemTokens(input []string, stemmer Stemmer) []string {
	if stemmer == nil {
		stemmer = IdentityStemmer{}
	}
	result := make([]string, len(input))
	for i, token := range input {
		result[i] = stemmer.Stem(token)
	}
	return result
}

func tokens(value string) []string {
	value = stripNoise(strings.ToLower(value))
	var result []string
	var token strings.Builder
	flush := func() {
		if token.Len() > 0 {
			result = append(result, token.String())
			token.Reset()
		}
	}
	for _, r := range value {
		if r == 'ё' {
			r = 'е'
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			token.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return result
}

func stripNoise(value string) string {
	fields := strings.Fields(value)
	kept := fields[:0]
	for _, field := range fields {
		lower := strings.ToLower(field)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "www.") || strings.HasPrefix(field, "@") {
			if last, ok := terminalPunctuation(field); ok {
				kept = append(kept, string(last))
			}
			continue
		}
		kept = append(kept, field)
	}
	return strings.Join(kept, " ")
}

func terminalPunctuation(value string) (rune, bool) {
	for _, suffix := range []rune{'.', '!', '?'} {
		if strings.HasSuffix(value, string(suffix)) {
			return suffix, true
		}
	}
	return 0, false
}
