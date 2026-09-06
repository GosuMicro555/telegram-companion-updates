package text

import "strings"

func Generate(value string, minN, maxN int) []string {
	if minN < 1 {
		minN = 1
	}
	if maxN > 4 {
		maxN = 4
	}
	if maxN < minN {
		return nil
	}
	var result []string
	for _, sentence := range Sentences(value) {
		for start := range sentence {
			for size := minN; size <= maxN && start+size <= len(sentence); size++ {
				result = append(result, strings.Join(sentence[start:start+size], " "))
			}
		}
	}
	return result
}
