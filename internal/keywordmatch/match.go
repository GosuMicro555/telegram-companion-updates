// Package keywordmatch matches keyword expressions against natural-language
// text. Expressions use the operators documented for Yandex Direct, while a
// plain multi-word expression keeps the application's unordered, same-sentence
// semantics.
package keywordmatch

// Match reports whether expression matches at least one sentence in text.
// Invalid and empty expressions never match.
func Match(expression, text string) bool {
	root, ok := parse(expression)
	if !ok {
		return false
	}
	for _, sentence := range tokenizeSentences(text) {
		for _, result := range root.eval(sentence, unordered) {
			if len(result) > 0 {
				return true
			}
		}
	}
	return false
}

// MatchesAny reports whether at least one expression matches text.
// Empty and invalid expressions are ignored.
func MatchesAny(expressions []string, text string) bool {
	for _, expression := range expressions {
		if Match(expression, text) {
			return true
		}
	}
	return false
}

// MatchesAnyExclusion reports whether at least one global exclusion matches.
// A standalone negative expression such as "-деньги" is treated like
// "деньги" because the caller has already established exclusion semantics.
func MatchesAnyExclusion(expressions []string, text string) bool {
	for _, expression := range expressions {
		if Match(expression, text) || matchStandaloneExclusion(expression, text) {
			return true
		}
	}
	return false
}

func matchStandaloneExclusion(expression, text string) bool {
	root, ok := parse(expression)
	if !ok {
		return false
	}
	for _, sentence := range tokenizeSentences(text) {
		negativeOnly, matched := matchNegativeOnly(root, sentence)
		if negativeOnly && matched {
			return true
		}
	}
	return false
}

func matchNegativeOnly(value node, sentence []word) (bool, bool) {
	switch current := value.(type) {
	case negativeNode:
		return true, hasNonEmptyMatch(current.child.eval(sentence, unordered))
	case sequenceNode:
		return matchNegativeChildren(current.children, sentence)
	case alternativeNode:
		return matchNegativeChildren(current.children, sentence)
	default:
		return false, false
	}
}

func matchNegativeChildren(children []node, sentence []word) (bool, bool) {
	if len(children) == 0 {
		return false, false
	}
	matched := false
	for _, child := range children {
		negativeOnly, childMatched := matchNegativeOnly(child, sentence)
		if !negativeOnly {
			return false, false
		}
		matched = matched || childMatched
	}
	return true, matched
}

type matchMode uint8

const (
	unordered matchMode = iota
	contiguous
)

type matchSet []int

type node interface {
	eval([]word, matchMode) []matchSet
	forcedTerms() []termNode
}

type termNode struct {
	value  string
	morph  string
	exact  bool
	forced bool
}

func (n termNode) eval(sentence []word, _ matchMode) []matchSet {
	if n.ignored() {
		return []matchSet{{}}
	}
	results := make([]matchSet, 0, 1)
	for index, candidate := range sentence {
		if n.matches(candidate) {
			results = append(results, matchSet{index})
		}
	}
	return results
}

func (n termNode) forcedTerms() []termNode {
	if n.forced || n.exact {
		return []termNode{n}
	}
	return nil
}

func (n termNode) ignored() bool {
	return !n.forced && !n.exact && isServiceWord(n.value)
}

func (n termNode) matches(candidate word) bool {
	if n.exact {
		return candidate.surface == n.value
	}
	return candidate.morph == n.morph
}

type sequenceNode struct {
	children []node
}

func (n sequenceNode) eval(sentence []word, mode matchMode) []matchSet {
	positive := make([]node, 0, len(n.children))
	for _, child := range n.children {
		if excluded, ok := child.(negativeNode); ok {
			if hasNonEmptyMatch(excluded.child.eval(sentence, unordered)) {
				return nil
			}
			continue
		}
		positive = append(positive, child)
	}
	if len(positive) == 0 {
		return nil
	}

	results := []matchSet{{}}
	for _, child := range positive {
		childResults := child.eval(sentence, mode)
		if len(childResults) == 0 {
			return nil
		}
		results = combine(results, childResults, mode)
		if len(results) == 0 {
			return nil
		}
	}
	return deduplicate(results)
}

func (n sequenceNode) forcedTerms() []termNode {
	var result []termNode
	for _, child := range n.children {
		result = append(result, child.forcedTerms()...)
	}
	return result
}

type alternativeNode struct {
	children []node
}

func (n alternativeNode) eval(sentence []word, mode matchMode) []matchSet {
	var result []matchSet
	for _, child := range n.children {
		result = append(result, child.eval(sentence, mode)...)
	}
	return deduplicate(result)
}

func (n alternativeNode) forcedTerms() []termNode {
	var result []termNode
	for _, child := range n.children {
		result = append(result, child.forcedTerms()...)
	}
	return result
}

type negativeNode struct {
	child node
}

func (n negativeNode) eval(sentence []word, mode matchMode) []matchSet {
	return n.child.eval(sentence, mode)
}

func (n negativeNode) forcedTerms() []termNode {
	return n.child.forcedTerms()
}

type quotedNode struct {
	child node
}

func (n quotedNode) eval(sentence []word, _ matchMode) []matchSet {
	filtered, indexes := significantWords(sentence, n.child.forcedTerms())
	if len(filtered) == 0 {
		return nil
	}
	var result []matchSet
	for _, candidate := range n.child.eval(filtered, unordered) {
		if len(candidate) != len(filtered) || !coversAll(candidate, len(filtered)) {
			continue
		}
		result = append(result, remap(candidate, indexes))
	}
	return deduplicate(result)
}

func (n quotedNode) forcedTerms() []termNode {
	return n.child.forcedTerms()
}

type bracketNode struct {
	child node
}

func (n bracketNode) eval(sentence []word, _ matchMode) []matchSet {
	return deduplicate(n.child.eval(sentence, contiguous))
}

func (n bracketNode) forcedTerms() []termNode {
	return n.child.forcedTerms()
}

func combine(left, right []matchSet, mode matchMode) []matchSet {
	var result []matchSet
	for _, a := range left {
		for _, b := range right {
			if overlaps(a, b) {
				continue
			}
			if mode == contiguous && len(a) > 0 && len(b) > 0 && maximum(a)+1 != minimum(b) {
				continue
			}
			result = append(result, merge(a, b))
		}
	}
	return result
}

func hasNonEmptyMatch(results []matchSet) bool {
	for _, result := range results {
		if len(result) > 0 {
			return true
		}
	}
	return false
}

func coversAll(result matchSet, size int) bool {
	if len(result) != size {
		return false
	}
	seen := make([]bool, size)
	for _, index := range result {
		if index < 0 || index >= size || seen[index] {
			return false
		}
		seen[index] = true
	}
	return true
}

func overlaps(left, right matchSet) bool {
	for _, a := range left {
		for _, b := range right {
			if a == b {
				return true
			}
		}
	}
	return false
}

func merge(left, right matchSet) matchSet {
	result := make(matchSet, 0, len(left)+len(right))
	result = append(result, left...)
	result = append(result, right...)
	return result
}

func minimum(values matchSet) int {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func maximum(values matchSet) int {
	result := values[0]
	for _, value := range values[1:] {
		if value > result {
			result = value
		}
	}
	return result
}

func remap(values matchSet, indexes []int) matchSet {
	result := make(matchSet, 0, len(values))
	for _, value := range values {
		if value >= 0 && value < len(indexes) {
			result = append(result, indexes[value])
		}
	}
	return result
}

func deduplicate(values []matchSet) []matchSet {
	seen := make(map[string]struct{}, len(values))
	result := make([]matchSet, 0, len(values))
	for _, value := range values {
		key := setKey(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func setKey(value matchSet) string {
	if len(value) == 0 {
		return "_"
	}
	used := make(map[int]struct{}, len(value))
	key := ""
	for len(used) < len(value) {
		next := -1
		for _, candidate := range value {
			if _, ok := used[candidate]; ok {
				continue
			}
			if next == -1 || candidate < next {
				next = candidate
			}
		}
		used[next] = struct{}{}
		key += string(rune(next+1)) + ","
	}
	return key
}

func significantWords(sentence []word, forced []termNode) ([]word, []int) {
	filtered := make([]word, 0, len(sentence))
	indexes := make([]int, 0, len(sentence))
	for index, candidate := range sentence {
		if isServiceWord(candidate.surface) && !matchesForced(candidate, forced) {
			continue
		}
		filtered = append(filtered, candidate)
		indexes = append(indexes, index)
	}
	return filtered, indexes
}

func matchesForced(candidate word, forced []termNode) bool {
	for _, term := range forced {
		if term.matches(candidate) {
			return true
		}
	}
	return false
}
