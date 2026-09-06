package analytics

import (
	"strings"

	analytictext "telegram-companion/internal/analytics/text"
)

type Profile struct {
	ID               string
	Name             string
	PositiveExamples []string
	Exclusions       []string
}

func DefaultMoneyShortageProfile() *Profile {
	return &Profile{
		ID:   "money-shortage",
		Name: "Нехватка денег",
		PositiveExamples: []string{
			"не хватает денег", "нет денег", "не могу позволить", "слишком дорого", "нужны деньги",
		},
		Exclusions: []string{"не проблема", "денег хватает", "не нужны деньги"},
	}
}

func (p Profile) Score(tokens []string) float64 {
	value := strings.Join(tokens, " ")
	normalized := analytictext.Normalize(value)
	for _, exclusion := range p.Exclusions {
		if strings.Contains(normalized, analytictext.Normalize(exclusion)) {
			return 0
		}
	}
	var score float64
	for _, example := range p.PositiveExamples {
		example = analytictext.Normalize(example)
		if normalized == example {
			score += 1
			continue
		}
		for _, token := range strings.Fields(example) {
			if containsToken(tokens, token) {
				score += 0.2
			}
		}
	}
	return score
}

func containsToken(tokens []string, wanted string) bool {
	for _, token := range tokens {
		if token == wanted {
			return true
		}
	}
	return false
}
