// Label selector parser + evaluator. Phase 22.8 PR β.
//
// Subset of the Kubernetes label selector grammar — enough to cover
// every group_role / credential-binding scoping case we have in mind
// without dragging in the full K8s expression engine.
//
// Grammar (whitespace tolerated everywhere):
//
//   selector := requirement (',' requirement)*
//   requirement := key '=' value
//                | key '!=' value
//                | key                   ; key exists
//                | '!' key               ; key does not exist
//
// All requirements are AND'd. There's no OR (use multiple group_roles
// or multiple secret bindings). No `in (...)` / `notin (...)` in v1
// — add later if a real operator need shows up.
//
// Empty selector strings parse to a special "match all" selector.
// That's how PR α's CP-wide grants stay valid — they have selector=NULL
// in the database, the API normalises NULL→"" on the way out, and a
// "" selector accepts every label map.

package db

import (
	"errors"
	"fmt"
	"strings"
)

// Selector is a parsed label selector. Use Parse to construct one.
type Selector struct {
	// matchAll is true when the selector text was empty. Short-
	// circuits Matches without iterating requirements.
	matchAll bool
	reqs     []requirement
}

type reqOp int

const (
	opEqual    reqOp = iota // key=value
	opNotEqual              // key!=value
	opExists                // key
	opNotExist              // !key
)

type requirement struct {
	op    reqOp
	key   string
	value string // empty for opExists / opNotExist
}

// MatchAll returns a selector that accepts every label map. Equivalent
// to ParseSelector("").
func MatchAll() Selector {
	return Selector{matchAll: true}
}

// ParseSelector tokenises and parses a selector string. Accepts the
// empty string as "match all". Returns errors with positional context
// so the UI can highlight where the user went wrong.
func ParseSelector(s string) (Selector, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Selector{matchAll: true}, nil
	}
	out := Selector{}
	// Split on commas at the top level. Values can't contain commas
	// in v1 (no quoting) so a naive split is safe.
	for _, raw := range strings.Split(s, ",") {
		req, err := parseRequirement(strings.TrimSpace(raw))
		if err != nil {
			return Selector{}, err
		}
		out.reqs = append(out.reqs, req)
	}
	return out, nil
}

// parseRequirement handles one comma-separated piece. The order of
// checks below matters because '!=' is a prefix of '!' otherwise; we
// look for the longer token first.
func parseRequirement(s string) (requirement, error) {
	if s == "" {
		return requirement{}, errors.New("empty requirement")
	}
	// `!key` (key does not exist)
	if strings.HasPrefix(s, "!") {
		key := strings.TrimSpace(s[1:])
		if !validKey(key) {
			return requirement{}, fmt.Errorf("bad key in %q", s)
		}
		return requirement{op: opNotExist, key: key}, nil
	}
	// `key!=value`
	if i := strings.Index(s, "!="); i >= 0 {
		key := strings.TrimSpace(s[:i])
		val := strings.TrimSpace(s[i+2:])
		if !validKey(key) || !validValue(val) {
			return requirement{}, fmt.Errorf("bad key/value in %q", s)
		}
		return requirement{op: opNotEqual, key: key, value: val}, nil
	}
	// `key=value`
	if i := strings.Index(s, "="); i >= 0 {
		key := strings.TrimSpace(s[:i])
		val := strings.TrimSpace(s[i+1:])
		if !validKey(key) || !validValue(val) {
			return requirement{}, fmt.Errorf("bad key/value in %q", s)
		}
		return requirement{op: opEqual, key: key, value: val}, nil
	}
	// bare `key` (exists)
	if !validKey(s) {
		return requirement{}, fmt.Errorf("bad key in %q", s)
	}
	return requirement{op: opExists, key: s}, nil
}

// Matches reports whether the given label map satisfies every
// requirement. A match-all selector returns true for any map,
// including nil.
func (sel Selector) Matches(labels map[string]string) bool {
	if sel.matchAll {
		return true
	}
	for _, req := range sel.reqs {
		v, present := labels[req.key]
		switch req.op {
		case opEqual:
			if !present || v != req.value {
				return false
			}
		case opNotEqual:
			// `key!=value` matches when the key is absent OR the value
			// differs. Mirrors K8s semantics (a missing key satisfies
			// the inequality).
			if present && v == req.value {
				return false
			}
		case opExists:
			if !present {
				return false
			}
		case opNotExist:
			if present {
				return false
			}
		}
	}
	return true
}

// String reproduces a canonical form of the selector — useful for
// logs and the UI's "current scope" indicator. Reads the same as
// what the parser would accept.
func (sel Selector) String() string {
	if sel.matchAll {
		return ""
	}
	parts := make([]string, 0, len(sel.reqs))
	for _, r := range sel.reqs {
		switch r.op {
		case opEqual:
			parts = append(parts, r.key+"="+r.value)
		case opNotEqual:
			parts = append(parts, r.key+"!="+r.value)
		case opExists:
			parts = append(parts, r.key)
		case opNotExist:
			parts = append(parts, "!"+r.key)
		}
	}
	return strings.Join(parts, ",")
}

// IsEmpty reports whether the selector matches everything (i.e. came
// from an empty string). The HasEffectiveRoleOnNode helper uses this
// to short-circuit a labels lookup when the grant is CP-wide.
func (sel Selector) IsEmpty() bool { return sel.matchAll }

// validKey enforces a sane character class on label keys: letters,
// digits, dot, dash, underscore, slash. Mirrors K8s label naming
// without the prefix-namespace requirement (we don't have multi-
// tenant operator UIs that would benefit from it).
func validKey(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '/':
		default:
			return false
		}
	}
	return true
}

// validValue is laxer than validKey — values can include spaces?
// We choose no, to keep the parser simple. Comma is forbidden because
// it terminates a requirement. Equals signs are forbidden inside the
// value because we use them as the operator separator.
func validValue(s string) bool {
	if s == "" {
		// Empty value is fine: `env=` matches when the label exists
		// with an empty value (rare; we tolerate it).
		return true
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '/':
		default:
			return false
		}
	}
	return true
}
