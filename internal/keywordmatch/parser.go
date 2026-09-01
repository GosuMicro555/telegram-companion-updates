package keywordmatch

import (
	"strings"
	"unicode"
)

type tokenKind uint8

const (
	tokenEOF tokenKind = iota
	tokenWord
	tokenBang
	tokenPlus
	tokenMinus
	tokenQuote
	tokenLeftBracket
	tokenRightBracket
	tokenLeftParen
	tokenRightParen
	tokenPipe
)

type token struct {
	kind  tokenKind
	value string
}

type parser struct {
	tokens []token
	index  int
}

func parse(expression string) (node, bool) {
	tokens, ok := lex(expression)
	if !ok {
		return nil, false
	}
	p := parser{tokens: append(tokens, token{kind: tokenEOF})}
	result, ok := p.parseAlternatives(tokenEOF)
	if !ok || p.current().kind != tokenEOF || result == nil {
		return nil, false
	}
	return result, true
}

func (p *parser) parseAlternatives(end tokenKind) (node, bool) {
	first, ok := p.parseSequence(end)
	if !ok {
		return nil, false
	}
	alternatives := []node{first}
	for p.current().kind == tokenPipe {
		p.index++
		next, ok := p.parseSequence(end)
		if !ok {
			return nil, false
		}
		alternatives = append(alternatives, next)
	}
	if len(alternatives) == 1 {
		return alternatives[0], true
	}
	return alternativeNode{children: alternatives}, true
}

func (p *parser) parseSequence(end tokenKind) (node, bool) {
	var children []node
	for {
		kind := p.current().kind
		if kind == end || kind == tokenPipe {
			break
		}
		if kind == tokenEOF || kind == tokenRightParen || kind == tokenRightBracket {
			return nil, false
		}
		child, ok := p.parseUnary()
		if !ok {
			return nil, false
		}
		children = append(children, child)
	}
	if len(children) == 0 {
		return nil, false
	}
	return sequenceNode{children: children}, true
}

func (p *parser) parseUnary() (node, bool) {
	excluded := false
	if p.current().kind == tokenMinus {
		excluded = true
		p.index++
	}
	child, ok := p.parsePrimary()
	if !ok {
		return nil, false
	}
	if excluded {
		child = forceServiceWords(child)
		return negativeNode{child: child}, true
	}
	return child, true
}

func (p *parser) parsePrimary() (node, bool) {
	exact := false
	forced := false
	for p.current().kind == tokenBang || p.current().kind == tokenPlus {
		if p.current().kind == tokenBang {
			if exact {
				return nil, false
			}
			exact = true
		} else {
			if forced {
				return nil, false
			}
			forced = true
		}
		p.index++
	}
	if exact || forced {
		if p.current().kind != tokenWord {
			return nil, false
		}
	}

	switch current := p.current(); current.kind {
	case tokenWord:
		p.index++
		value := normalizeSurface(current.value)
		if value == "" {
			return nil, false
		}
		return termNode{value: value, morph: morphologyKey(value), exact: exact, forced: forced}, true
	case tokenQuote:
		if exact || forced {
			return nil, false
		}
		p.index++
		child, ok := p.parseAlternatives(tokenQuote)
		if !ok || p.current().kind != tokenQuote {
			return nil, false
		}
		p.index++
		return quotedNode{child: child}, true
	case tokenLeftBracket:
		if exact || forced {
			return nil, false
		}
		p.index++
		child, ok := p.parseAlternatives(tokenRightBracket)
		if !ok || p.current().kind != tokenRightBracket {
			return nil, false
		}
		p.index++
		return bracketNode{child: forceServiceWords(child)}, true
	case tokenLeftParen:
		if exact || forced {
			return nil, false
		}
		p.index++
		child, ok := p.parseAlternatives(tokenRightParen)
		if !ok || p.current().kind != tokenRightParen {
			return nil, false
		}
		p.index++
		return child, true
	default:
		return nil, false
	}
}

func (p *parser) current() token {
	if p.index >= len(p.tokens) {
		return token{kind: tokenEOF}
	}
	return p.tokens[p.index]
}

func forceServiceWords(value node) node {
	switch current := value.(type) {
	case termNode:
		current.forced = true
		return current
	case sequenceNode:
		for index, child := range current.children {
			current.children[index] = forceServiceWords(child)
		}
		return current
	case alternativeNode:
		for index, child := range current.children {
			current.children[index] = forceServiceWords(child)
		}
		return current
	case quotedNode:
		current.child = forceServiceWords(current.child)
		return current
	case bracketNode:
		current.child = forceServiceWords(current.child)
		return current
	default:
		return value
	}
}

func lex(expression string) ([]token, bool) {
	runes := []rune(strings.TrimSpace(expression))
	if len(runes) == 0 {
		return nil, false
	}
	result := make([]token, 0, len(runes)/2)
	var value strings.Builder
	flush := func() {
		if value.Len() == 0 {
			return
		}
		result = append(result, token{kind: tokenWord, value: value.String()})
		value.Reset()
	}
	for index, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			value.WriteRune(r)
			continue
		}
		flush()
		switch r {
		case '!':
			result = append(result, token{kind: tokenBang})
		case '+':
			result = append(result, token{kind: tokenPlus})
		case '-':
			if operatorBoundary(runes, index) {
				result = append(result, token{kind: tokenMinus})
			}
		case '"', '«', '»':
			result = append(result, token{kind: tokenQuote})
		case '[':
			result = append(result, token{kind: tokenLeftBracket})
		case ']':
			result = append(result, token{kind: tokenRightBracket})
		case '(':
			result = append(result, token{kind: tokenLeftParen})
		case ')':
			result = append(result, token{kind: tokenRightParen})
		case '|':
			result = append(result, token{kind: tokenPipe})
		}
	}
	flush()
	return result, len(result) > 0
}

func operatorBoundary(runes []rune, index int) bool {
	if index == 0 {
		return true
	}
	previous := runes[index-1]
	return unicode.IsSpace(previous) || strings.ContainsRune("([|\"«", previous)
}
