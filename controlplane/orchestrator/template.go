// Template engine for orchestration specs.
//
// Two operations:
//   Render — replace `{{ expr }}` substitutions in a prompt/node/etc.
//            with stringified values.
//   Eval   — evaluate a single expression to a Go value (used by
//            `when` step gates and `trigger.filter`).
//
// The language is intentionally small: dotted-path access, pipe-chained
// filters, comparison + boolean + membership operators, list literals.
// Anything beyond that should be expressed inside the agent prompt or
// as a richer agent — the templating is glue, not a programming
// language.
//
// Grammar (informal):
//
//   or      := and ("||" and)*
//   and     := compare ("&&" compare)*
//   compare := pipe (CMP_OP pipe)?
//   pipe    := unary ("|" filter)*
//   unary   := "!" unary | primary
//   primary := "(" or ")" | literal | path | list
//   path    := IDENT ("." IDENT)*
//   list    := "[" or ("," or)* "]"
//   filter  := IDENT ("(" args? ")")?
//   arg     := IDENT "=" literal | or
//   CMP_OP  := "==" | "!=" | "<" | "<=" | ">" | ">=" | "in"
package orchestrator

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

// Env is the binding environment a template runs against. Keys are
// step ids (and the reserved name "trigger"); values can be primitives,
// maps, slices, or structs — the path walker uses reflection.
type Env map[string]any

// Render substitutes every `{{ expr }}` in s with the stringified value
// of expr. Plain text outside braces is preserved verbatim. Errors in
// any single expression abort the whole render — operators want to know
// when they typoed a field rather than getting silent empties.
func Render(s string, env Env) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		// Find the next "{{". Anything before it is literal text.
		open := strings.Index(s[i:], "{{")
		if open < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+open])
		i += open + 2
		// Find the matching "}}". We don't allow nesting.
		close := strings.Index(s[i:], "}}")
		if close < 0 {
			return "", fmt.Errorf("template: unterminated `{{` near offset %d", i-2)
		}
		expr := strings.TrimSpace(s[i : i+close])
		i += close + 2
		val, err := Eval(expr, env)
		if err != nil {
			return "", fmt.Errorf("template: %q: %w", expr, err)
		}
		b.WriteString(stringify(val))
	}
	return b.String(), nil
}

// Eval parses and evaluates a single expression. Used directly by the
// engine for `when` gates — those are written as bare expressions
// (without `{{ }}`) and must coerce to bool.
func Eval(expr string, env Env) (any, error) {
	// Strip a single layer of `{{ }}` if the caller wrote
	// `when: "{{ foo > 0 }}"` instead of `when: "foo > 0"`. This
	// matches what operators are likely to write.
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "{{") && strings.HasSuffix(expr, "}}") {
		expr = strings.TrimSpace(expr[2 : len(expr)-2])
	}
	tokens, err := lex(expr)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if !p.atEnd() {
		return nil, fmt.Errorf("unexpected token %q at end", p.peek().lit)
	}
	return eval(node, env)
}

// EvalBool is Eval coerced to bool — convenience for the engine's
// `when` and `trigger.filter` checks.
func EvalBool(expr string, env Env) (bool, error) {
	v, err := Eval(expr, env)
	if err != nil {
		return false, err
	}
	return truthy(v), nil
}

// ── lexer ────────────────────────────────────────────────────────────

type tokKind int

const (
	tEOF tokKind = iota
	tIDENT
	tNUMBER
	tSTRING
	tBOOL
	tDOT
	tCOMMA
	tLBRACKET
	tRBRACKET
	tLPAREN
	tRPAREN
	tPIPE
	tBANG
	tEQ
	tNE
	tLT
	tLE
	tGT
	tGE
	tAND
	tOR
	tASSIGN   // single `=` inside filter-call kwargs
	tIN       // keyword
	tCONTAINS // keyword — strings.Contains for strings, slice membership for arrays
)

type token struct {
	kind tokKind
	lit  string // raw literal for error messages
	val  any    // pre-decoded value for NUMBER/STRING/BOOL
}

func lex(s string) ([]token, error) {
	var out []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case unicode.IsSpace(rune(c)):
			i++
		case c == '.':
			out = append(out, token{kind: tDOT, lit: "."}); i++
		case c == ',':
			out = append(out, token{kind: tCOMMA, lit: ","}); i++
		case c == '[':
			out = append(out, token{kind: tLBRACKET, lit: "["}); i++
		case c == ']':
			out = append(out, token{kind: tRBRACKET, lit: "]"}); i++
		case c == '(':
			out = append(out, token{kind: tLPAREN, lit: "("}); i++
		case c == ')':
			out = append(out, token{kind: tRPAREN, lit: ")"}); i++
		case c == '|':
			if i+1 < len(s) && s[i+1] == '|' {
				out = append(out, token{kind: tOR, lit: "||"}); i += 2
			} else {
				out = append(out, token{kind: tPIPE, lit: "|"}); i++
			}
		case c == '&':
			if i+1 < len(s) && s[i+1] == '&' {
				out = append(out, token{kind: tAND, lit: "&&"}); i += 2
			} else {
				return nil, fmt.Errorf("unexpected `&` (use `&&`)")
			}
		case c == '!':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, token{kind: tNE, lit: "!="}); i += 2
			} else {
				out = append(out, token{kind: tBANG, lit: "!"}); i++
			}
		case c == '=':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, token{kind: tEQ, lit: "=="}); i += 2
			} else {
				out = append(out, token{kind: tASSIGN, lit: "="}); i++
			}
		case c == '<':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, token{kind: tLE, lit: "<="}); i += 2
			} else {
				out = append(out, token{kind: tLT, lit: "<"}); i++
			}
		case c == '>':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, token{kind: tGE, lit: ">="}); i += 2
			} else {
				out = append(out, token{kind: tGT, lit: ">"}); i++
			}
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' && j+1 < len(s) {
					j += 2
					continue
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string at offset %d", i)
			}
			raw := s[i+1 : j]
			// Minimal escape handling: \\ \" \' \n \t.
			raw = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\'`, `'`, `\n`, "\n", `\t`, "\t").Replace(raw)
			out = append(out, token{kind: tSTRING, lit: s[i : j+1], val: raw})
			i = j + 1
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && (s[j] == '.' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			lit := s[i:j]
			if strings.Contains(lit, ".") {
				f, err := strconv.ParseFloat(lit, 64)
				if err != nil {
					return nil, err
				}
				out = append(out, token{kind: tNUMBER, lit: lit, val: f})
			} else {
				n, err := strconv.ParseInt(lit, 10, 64)
				if err != nil {
					return nil, err
				}
				out = append(out, token{kind: tNUMBER, lit: lit, val: n})
			}
			i = j
		case isIdentStart(c):
			j := i
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			lit := s[i:j]
			switch lit {
			case "true":
				out = append(out, token{kind: tBOOL, lit: lit, val: true})
			case "false":
				out = append(out, token{kind: tBOOL, lit: lit, val: false})
			case "in":
				out = append(out, token{kind: tIN, lit: lit})
			case "contains":
				out = append(out, token{kind: tCONTAINS, lit: lit})
			default:
				out = append(out, token{kind: tIDENT, lit: lit})
			}
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at offset %d", c, i)
		}
	}
	out = append(out, token{kind: tEOF, lit: ""})
	return out, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentChar(c byte) bool {
	return c == '_' || c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// ── parser (Pratt-style recursive descent) ───────────────────────────

type node interface{}

type nLiteral struct{ v any }
type nList struct{ items []node }
type nPath struct{ parts []string }
type nUnary struct{ op string; rhs node }
type nBinary struct{ op string; lhs, rhs node }
type nPipe struct{ value node; filters []nFilter }
type nFilter struct{ name string; args []nFilterArg }
type nFilterArg struct {
	key   string // "" for positional
	value node
}

type parser struct {
	tokens []token
	idx    int
}

func (p *parser) peek() token { return p.tokens[p.idx] }
func (p *parser) atEnd() bool { return p.peek().kind == tEOF }
func (p *parser) advance() token {
	t := p.tokens[p.idx]
	if t.kind != tEOF {
		p.idx++
	}
	return t
}
func (p *parser) expect(k tokKind, ctx string) (token, error) {
	if p.peek().kind != k {
		return token{}, fmt.Errorf("expected %s, got %q", ctx, p.peek().lit)
	}
	return p.advance(), nil
}

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tOR {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = nBinary{op: "||", lhs: left, rhs: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseCompare()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tAND {
		p.advance()
		right, err := p.parseCompare()
		if err != nil {
			return nil, err
		}
		left = nBinary{op: "&&", lhs: left, rhs: right}
	}
	return left, nil
}

var cmpOpForKind = map[tokKind]string{
	tEQ: "==", tNE: "!=", tLT: "<", tLE: "<=", tGT: ">", tGE: ">=", tIN: "in", tCONTAINS: "contains",
}

func (p *parser) parseCompare() (node, error) {
	left, err := p.parsePipe()
	if err != nil {
		return nil, err
	}
	if op, ok := cmpOpForKind[p.peek().kind]; ok {
		p.advance()
		right, err := p.parsePipe()
		if err != nil {
			return nil, err
		}
		left = nBinary{op: op, lhs: left, rhs: right}
	}
	return left, nil
}

func (p *parser) parsePipe() (node, error) {
	val, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tPIPE {
		return val, nil
	}
	pn := nPipe{value: val}
	for p.peek().kind == tPIPE {
		p.advance()
		f, err := p.parseFilter()
		if err != nil {
			return nil, err
		}
		pn.filters = append(pn.filters, f)
	}
	return pn, nil
}

func (p *parser) parseFilter() (nFilter, error) {
	tok, err := p.expect(tIDENT, "filter name")
	if err != nil {
		return nFilter{}, err
	}
	f := nFilter{name: tok.lit}
	if p.peek().kind != tLPAREN {
		return f, nil
	}
	p.advance() // consume `(`
	if p.peek().kind == tRPAREN {
		p.advance()
		return f, nil
	}
	for {
		arg, err := p.parseFilterArg()
		if err != nil {
			return nFilter{}, err
		}
		f.args = append(f.args, arg)
		if p.peek().kind == tCOMMA {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(tRPAREN, "`)`"); err != nil {
		return nFilter{}, err
	}
	return f, nil
}

func (p *parser) parseFilterArg() (nFilterArg, error) {
	// Peek for `IDENT =` — if matched, it's a kwarg.
	if p.peek().kind == tIDENT && p.idx+1 < len(p.tokens) && p.tokens[p.idx+1].kind == tASSIGN {
		key := p.advance().lit
		p.advance() // consume `=`
		val, err := p.parseOr()
		if err != nil {
			return nFilterArg{}, err
		}
		return nFilterArg{key: key, value: val}, nil
	}
	val, err := p.parseOr()
	if err != nil {
		return nFilterArg{}, err
	}
	return nFilterArg{value: val}, nil
}

func (p *parser) parseUnary() (node, error) {
	if p.peek().kind == tBANG {
		p.advance()
		rhs, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return nUnary{op: "!", rhs: rhs}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (node, error) {
	tok := p.peek()
	switch tok.kind {
	case tLPAREN:
		p.advance()
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tRPAREN, "`)`"); err != nil {
			return nil, err
		}
		return expr, nil
	case tNUMBER, tSTRING, tBOOL:
		p.advance()
		return nLiteral{v: tok.val}, nil
	case tLBRACKET:
		p.advance()
		var items []node
		if p.peek().kind != tRBRACKET {
			for {
				it, err := p.parseOr()
				if err != nil {
					return nil, err
				}
				items = append(items, it)
				if p.peek().kind == tCOMMA {
					p.advance()
					continue
				}
				break
			}
		}
		if _, err := p.expect(tRBRACKET, "`]`"); err != nil {
			return nil, err
		}
		return nList{items: items}, nil
	case tIDENT:
		// Path: IDENT (DOT IDENT)*. Numbers can also appear as path
		// components for indexed access (`step.findings.0.title`).
		var parts []string
		parts = append(parts, p.advance().lit)
		for p.peek().kind == tDOT {
			p.advance()
			next := p.peek()
			switch next.kind {
			case tIDENT:
				parts = append(parts, p.advance().lit)
			case tNUMBER:
				parts = append(parts, p.advance().lit)
			default:
				return nil, fmt.Errorf("expected identifier after `.`, got %q", next.lit)
			}
		}
		return nPath{parts: parts}, nil
	}
	return nil, fmt.Errorf("unexpected token %q", tok.lit)
}

// ── evaluator ────────────────────────────────────────────────────────

func eval(n node, env Env) (any, error) {
	switch v := n.(type) {
	case nLiteral:
		return v.v, nil
	case nList:
		out := make([]any, 0, len(v.items))
		for _, it := range v.items {
			val, err := eval(it, env)
			if err != nil {
				return nil, err
			}
			out = append(out, val)
		}
		return out, nil
	case nPath:
		return resolvePath(env, v.parts)
	case nUnary:
		rhs, err := eval(v.rhs, env)
		if err != nil {
			return nil, err
		}
		switch v.op {
		case "!":
			return !truthy(rhs), nil
		}
	case nBinary:
		lhs, err := eval(v.lhs, env)
		if err != nil {
			return nil, err
		}
		// Short-circuit booleans before evaluating rhs.
		if v.op == "&&" && !truthy(lhs) {
			return false, nil
		}
		if v.op == "||" && truthy(lhs) {
			return true, nil
		}
		rhs, err := eval(v.rhs, env)
		if err != nil {
			return nil, err
		}
		return applyBinary(v.op, lhs, rhs)
	case nPipe:
		val, err := eval(v.value, env)
		if err != nil {
			return nil, err
		}
		for _, f := range v.filters {
			val, err = applyFilter(f, val, env)
			if err != nil {
				return nil, err
			}
		}
		return val, nil
	}
	return nil, fmt.Errorf("eval: unknown node %T", n)
}

// resolvePath walks dotted-path access through maps, slices, structs.
// Missing keys evaluate to nil rather than erroring — this lets a
// template with `{{step.findings.first.path}}` quietly become empty
// if the step had no findings, instead of forcing every prompt to
// be defensive.
func resolvePath(env Env, parts []string) (any, error) {
	if len(parts) == 0 {
		return nil, nil
	}
	v, ok := env[parts[0]]
	if !ok {
		return nil, nil
	}
	for _, key := range parts[1:] {
		v = walkOne(v, key)
		if v == nil {
			return nil, nil
		}
	}
	return v, nil
}

func walkOne(v any, key string) any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		// Map keys are usually strings; coerce to string lookup.
		mv := rv.MapIndex(reflect.ValueOf(key))
		if !mv.IsValid() {
			return nil
		}
		return mv.Interface()
	case reflect.Slice, reflect.Array:
		// Numeric keys → index. `first` / `last` are filter words,
		// not path keys (handled in applyFilter).
		idx, err := strconv.Atoi(key)
		if err != nil || idx < 0 || idx >= rv.Len() {
			// Allow `.first` and `.last` as sugar inside paths.
			switch key {
			case "first":
				if rv.Len() == 0 {
					return nil
				}
				return rv.Index(0).Interface()
			case "last":
				if rv.Len() == 0 {
					return nil
				}
				return rv.Index(rv.Len() - 1).Interface()
			case "length":
				return int64(rv.Len())
			}
			return nil
		}
		return rv.Index(idx).Interface()
	case reflect.Struct:
		// Try field by exact name, then case-insensitively. Keeps
		// templates readable when the Go side uses CamelCase.
		f := rv.FieldByName(key)
		if !f.IsValid() {
			f = rv.FieldByNameFunc(func(s string) bool {
				return strings.EqualFold(s, key)
			})
		}
		if !f.IsValid() {
			return nil
		}
		return f.Interface()
	}
	return nil
}

// ── filters ──────────────────────────────────────────────────────────

func applyFilter(f nFilter, value any, env Env) (any, error) {
	switch f.name {
	case "first":
		return firstOf(value), nil
	case "last":
		return lastOf(value), nil
	case "length":
		return int64(lengthOf(value)), nil
	case "json":
		b, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case "tail":
		n, err := intArg(f, env, 0)
		if err != nil {
			return nil, err
		}
		return tailLines(stringify(value), n), nil
	case "head":
		n, err := intArg(f, env, 0)
		if err != nil {
			return nil, err
		}
		return headLines(stringify(value), n), nil
	case "where":
		key, want, err := kwargFilter(f, env)
		if err != nil {
			return nil, err
		}
		return filterWhere(value, key, want), nil
	case "any":
		key, want, err := kwargFilter(f, env)
		if err != nil {
			return nil, err
		}
		return anyMatch(value, key, want), nil
	}
	return nil, fmt.Errorf("unknown filter %q", f.name)
}

// intArg pulls the Nth positional arg as int64 (default 0).
func intArg(f nFilter, env Env, idx int) (int, error) {
	if idx >= len(f.args) || f.args[idx].key != "" {
		return 0, fmt.Errorf("filter %s: positional integer arg required", f.name)
	}
	v, err := eval(f.args[idx].value, env)
	if err != nil {
		return 0, err
	}
	switch x := v.(type) {
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case float64:
		return int(x), nil
	}
	return 0, fmt.Errorf("filter %s: arg must be a number, got %T", f.name, v)
}

// kwargFilter pulls a single kwarg `(<key>=<value>)` for `where`/`any`.
func kwargFilter(f nFilter, env Env) (key string, want any, err error) {
	if len(f.args) != 1 || f.args[0].key == "" {
		return "", nil, fmt.Errorf("filter %s: expects a single kwarg, e.g. %s(field='value')", f.name, f.name)
	}
	key = f.args[0].key
	want, err = eval(f.args[0].value, env)
	return
}

func filterWhere(value any, key string, want any) any {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return []any{}
	}
	out := make([]any, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i).Interface()
		got := walkOne(item, key)
		if equalish(got, want) {
			out = append(out, item)
		}
	}
	return out
}

func anyMatch(value any, key string, want any) bool {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return false
	}
	for i := 0; i < rv.Len(); i++ {
		got := walkOne(rv.Index(i).Interface(), key)
		if equalish(got, want) {
			return true
		}
	}
	return false
}

func firstOf(v any) any {
	rv := reflect.ValueOf(v)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) && rv.Len() > 0 {
		return rv.Index(0).Interface()
	}
	return nil
}
func lastOf(v any) any {
	rv := reflect.ValueOf(v)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) && rv.Len() > 0 {
		return rv.Index(rv.Len() - 1).Interface()
	}
	return nil
}
func lengthOf(v any) int {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return 0
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.String, reflect.Map:
		return rv.Len()
	}
	return 0
}

func tailLines(s string, n int) string {
	if n <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
func headLines(s string, n int) string {
	if n <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

// ── operators + truthiness ──────────────────────────────────────────

func applyBinary(op string, lhs, rhs any) (any, error) {
	switch op {
	case "&&":
		return truthy(lhs) && truthy(rhs), nil
	case "||":
		return truthy(lhs) || truthy(rhs), nil
	case "==":
		return equalish(lhs, rhs), nil
	case "!=":
		return !equalish(lhs, rhs), nil
	case "<", "<=", ">", ">=":
		return compareNum(op, lhs, rhs)
	case "in":
		rv := reflect.ValueOf(rhs)
		if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
			return false, fmt.Errorf("`in` requires a list on the right, got %T", rhs)
		}
		for i := 0; i < rv.Len(); i++ {
			if equalish(lhs, rv.Index(i).Interface()) {
				return true, nil
			}
		}
		return false, nil
	case "contains":
		// Polymorphic: string-substring or slice-membership, picked
		// from the LHS shape. Mirrors the syntax operators reach for
		// instinctively in trigger filters
		// (`finding.title contains 'disk'`, `cpus contains 'arm64'`).
		if ls, ok := lhs.(string); ok {
			if rs, ok := rhs.(string); ok {
				return strings.Contains(ls, rs), nil
			}
			return false, fmt.Errorf("`contains` on a string needs a string rhs, got %T", rhs)
		}
		rv := reflect.ValueOf(lhs)
		if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
			for i := 0; i < rv.Len(); i++ {
				if equalish(rv.Index(i).Interface(), rhs) {
					return true, nil
				}
			}
			return false, nil
		}
		// Nil / missing → not contained. Filters often reference
		// optional fields like finding.attributes.sha256 that may
		// not be present; treat absence as "doesn't contain".
		if lhs == nil {
			return false, nil
		}
		return false, fmt.Errorf("`contains` lhs must be string or list, got %T", lhs)
	}
	return nil, fmt.Errorf("unknown operator %q", op)
}

func compareNum(op string, lhs, rhs any) (bool, error) {
	a, ok1 := toFloat(lhs)
	b, ok2 := toFloat(rhs)
	if !ok1 || !ok2 {
		return false, fmt.Errorf("operator %s requires numeric operands, got %T %s %T", op, lhs, op, rhs)
	}
	switch op {
	case "<":
		return a < b, nil
	case "<=":
		return a <= b, nil
	case ">":
		return a > b, nil
	case ">=":
		return a >= b, nil
	}
	return false, fmt.Errorf("unreachable")
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

func equalish(a, b any) bool {
	// Nil compares equal to nil, empty string, empty slice, and 0 —
	// the convention author-friendly filters expect when an
	// attribute is absent. Without this, `finding.attributes.foo
	// != ''` is *always* true on findings that don't carry the
	// attribute, which silently breaks every filter using that
	// pattern.
	if a == nil && b == nil {
		return true
	}
	if a == nil {
		return isZeroish(b)
	}
	if b == nil {
		return isZeroish(a)
	}
	// Numeric coercion so `1 == 1.0` and `"5" == 5` work.
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			return af == bf
		}
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// isZeroish — what counts as "empty enough" to compare equal to nil
// in a trigger filter. Mirrors the truthiness rules but stays
// strictly equality-shaped so nothing gets coerced.
func isZeroish(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case int:
		return x == 0
	case int64:
		return x == 0
	case float64:
		return x == 0
	}
	rv := reflect.ValueOf(v)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Map || rv.Kind() == reflect.Array) {
		return rv.Len() == 0
	}
	return false
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return false
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() > 0
	}
	return true
}

func stringify(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
		b, err := json.Marshal(v)
		if err == nil {
			return string(b)
		}
	}
	return fmt.Sprintf("%v", v)
}
