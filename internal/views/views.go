// Package views parses and evaluates the named-view predicates from config. A
// predicate is a boolean expression over an occurrence's owner and tags, for
// example: owner:brandon and not tag:work
//
// Grammar:
//
//	expr    := or
//	or      := and ("or" and)*
//	and     := unary ("and" unary)*
//	unary   := "not" unary | primary
//	primary := "(" expr ")" | atom
//	atom    := "tag:" WORD | "owner:" WORD
package views

import (
	"fmt"
	"strings"
)

// Subject is the occurrence context a predicate is evaluated against.
type Subject struct {
	Owner string
	Tags  []string
}

func (s Subject) hasTag(t string) bool {
	for _, x := range s.Tags {
		if x == t {
			return true
		}
	}
	return false
}

// Predicate reports whether a subject satisfies a view.
type Predicate func(Subject) bool

// Parse compiles a predicate string. An empty or malformed expression is an
// error.
func Parse(s string) (Predicate, error) {
	toks := tokenize(s)
	if len(toks) == 0 {
		return nil, fmt.Errorf("views: empty predicate")
	}
	p := &parser{toks: toks}
	pred, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("views: unexpected %q", p.toks[p.pos])
	}
	return pred, nil
}

func tokenize(s string) []string {
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '(' || r == ')':
			flush()
			toks = append(toks, string(r))
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return toks
}

type parser struct {
	toks []string
	pos  int
}

func (p *parser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *parser) next() string {
	t := p.peek()
	p.pos++
	return t
}

func (p *parser) parseExpr() (Predicate, error) { return p.parseOr() }

func (p *parser) parseOr() (Predicate, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for strings.EqualFold(p.peek(), "or") {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(s Subject) bool { return l(s) || r(s) }
	}
	return left, nil
}

func (p *parser) parseAnd() (Predicate, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for strings.EqualFold(p.peek(), "and") {
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(s Subject) bool { return l(s) && r(s) }
	}
	return left, nil
}

func (p *parser) parseUnary() (Predicate, error) {
	if strings.EqualFold(p.peek(), "not") {
		p.next()
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return func(s Subject) bool { return !inner(s) }, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Predicate, error) {
	switch t := p.peek(); {
	case t == "(":
		p.next()
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.next() != ")" {
			return nil, fmt.Errorf("views: missing closing parenthesis")
		}
		return inner, nil
	case t == "":
		return nil, fmt.Errorf("views: expected a term")
	case t == "and" || t == "or" || t == "not" || t == ")":
		return nil, fmt.Errorf("views: unexpected %q", t)
	default:
		p.next()
		return atom(t)
	}
}

func atom(t string) (Predicate, error) {
	key, val, ok := strings.Cut(t, ":")
	if !ok || val == "" {
		return nil, fmt.Errorf("views: term %q is not tag:<name> or owner:<name>", t)
	}
	switch strings.ToLower(key) {
	case "tag":
		return func(s Subject) bool { return s.hasTag(val) }, nil
	case "owner":
		return func(s Subject) bool { return s.Owner == val }, nil
	default:
		return nil, fmt.Errorf("views: unknown selector %q (want tag or owner)", key)
	}
}
