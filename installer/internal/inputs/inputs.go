// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package inputs declares every value the install wizard asks for, with the
// rules it validates them by, as plain data: a name, the screen, the label,
// the default, and rules such as "required" or "matches this pattern".
//
// It knows nothing about any particular consumer. Validate checks values
// against the rules in Go; other tools read the same data and translate each
// rule kind into their own language. Keep it that way: nothing here should
// name a file format or a tool, and the installer never needs the tools.
package inputs

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ai-on-gke/substrate-gke/installer/internal/state"
)

// Kind is an input's type.
type Kind int

const (
	// String inputs hold Go strings.
	String Kind = iota
	// Int inputs hold whole numbers (Go ints).
	Int
	// Bool inputs hold Go bools.
	Bool
)

// Values holds input values keyed by Input.Name. A value missing from it reads
// as the input's default.
type Values map[string]any

// Input is one value the wizard asks for.
type Input struct {
	// Name identifies the input: the key in Values, in snake_case.
	Name string
	// Screen is the wizard step that asks for it.
	Screen state.Step
	// Label and Placeholder are what the wizard shows for the field.
	Label       string
	Placeholder string
	// Description says what the input is for.
	Description string
	Kind        Kind
	// Default is what the wizard does when the user just presses enter. nil
	// means there is none: the input is required.
	Default any
	// MaxLen is the longest string the wizard's text field takes (its
	// CharLimit), or for an Int the most digits; 0 means no limit.
	MaxLen int
	// Rules are checked in order; the first one that fails is reported.
	Rules []Rule
}

// RuleKind says what a Rule checks. Consumers switch on it.
type RuleKind int

const (
	// MaxLength: a String has at most Max characters (runes).
	MaxLength RuleKind = iota
	// Whole: an Int is a whole number.
	Whole
	// Required: a String is not empty or only whitespace.
	Required
	// Matches: a non-empty String matches the anchored RE2 Pattern.
	Matches
	// OneOf: a String is one of Choices.
	OneOf
	// IntBetween: an Int is within [Min, Max].
	IntBetween
	// IntAtLeastMaxOf: an Int is at least max(the input named Other, Min).
	IntAtLeastMaxOf
	// Custom: the check named Check (see CustomChecks) passes.
	Custom
)

// Rule is one validation rule, as data.
type Rule struct {
	Kind     RuleKind
	Pattern  string
	Choices  []string
	Min, Max int
	Other    string
	Check    string
	// Message is shown when the rule fails. It may contain the
	// placeholders listed under Placeholders.
	Message string
	// When, if set, restricts the rule to the case where it holds.
	When *Cond
}

// The message placeholders, and what they stand for.
const (
	// PlaceholderValue is the failing value as given.
	PlaceholderValue = "{value}"
	// PlaceholderQuoted is the failing value, quoted.
	PlaceholderQuoted = "{quoted}"
	// PlaceholderVersion is the Substrate version an image tag names (the
	// tag without any @digest), quoted.
	PlaceholderVersion = "{version}"
)

// Cond is an equality test on another input.
type Cond struct {
	Name  string
	Value any
}

// Is builds a Cond that holds when input name has value.
func Is(name string, value any) Cond { return Cond{name, value} }

// If restricts r to the case where c holds; otherwise it passes.
func (r Rule) If(c Cond) Rule {
	r.When = &c
	return r
}

// Applies reports whether r is in force for v (its When holds, or it has
// none).
func (r Rule) Applies(v Values) bool {
	return r.When == nil || Resolve(v)[r.When.Name] == r.When.Value
}

// CustomChecks are the Go halves of the Custom rules, by name. A consumer
// that translates rules must handle every name used here.
var CustomChecks = map[string]func(value string) bool{}

// Passes reports whether v satisfies r for input in. It does not consult
// r.When; see Applies.
func (r Rule) Passes(in *Input, v Values) bool {
	v = Resolve(v)
	switch r.Kind {
	case MaxLength:
		return utf8.RuneCountInString(str(v, in.Name)) <= r.Max
	case Whole:
		n := num(v, in.Name)
		return n == math.Trunc(n)
	case Required:
		return strings.TrimSpace(str(v, in.Name)) != ""
	case Matches:
		s := str(v, in.Name)
		return s == "" || regexp.MustCompile(r.Pattern).MatchString(s)
	case OneOf:
		s := str(v, in.Name)
		for _, c := range r.Choices {
			if s == c {
				return true
			}
		}
		return false
	case IntBetween:
		n := num(v, in.Name)
		return n >= float64(r.Min) && n <= float64(r.Max)
	case IntAtLeastMaxOf:
		return num(v, in.Name) >= math.Max(num(v, r.Other), float64(r.Min))
	case Custom:
		check, ok := CustomChecks[r.Check]
		if !ok {
			panic("inputs: no custom check " + strconv.Quote(r.Check))
		}
		return check(str(v, in.Name))
	}
	panic(fmt.Sprintf("inputs: unknown rule kind %d", r.Kind))
}

// Text is r's message with its placeholders filled in from v.
func (r Rule) Text(in *Input, v Values) string {
	v = Resolve(v)
	s := fmt.Sprint(v[in.Name])
	return strings.NewReplacer(
		PlaceholderValue, s,
		PlaceholderQuoted, strconv.Quote(s),
		PlaceholderVersion, strconv.Quote(ImageVersion(s)),
	).Replace(r.Message)
}

// AllRules are the rules every input gets from its own fields (the text
// field's character limit; for an Int, that it is whole), then the declared
// ones, in the order they are checked.
func (in *Input) AllRules() []Rule {
	var rules []Rule
	switch {
	case in.Kind == String && in.MaxLen > 0:
		rules = append(rules, Rule{Kind: MaxLength, Max: in.MaxLen,
			Message: fmt.Sprintf("Must be at most %d characters, the most the wizard's field takes.", in.MaxLen)})
	case in.Kind == Int:
		rules = append(rules, Rule{Kind: Whole, Message: "Must be a whole number."})
	}
	return append(rules, in.Rules...)
}

// Problem is the first rule an input failed.
type Problem struct {
	Input   *Input
	Rule    int // index into Input.AllRules()
	Message string
}

func (p *Problem) Error() string { return p.Input.Name + ": " + p.Message }

// Resolve fills in the default of every input v lacks.
func Resolve(v Values) Values {
	out := Values{}
	for _, in := range All {
		if val, ok := v[in.Name]; ok {
			out[in.Name] = val
			continue
		}
		switch {
		case in.Default != nil:
			out[in.Name] = in.Default
		case in.Kind == String:
			out[in.Name] = ""
		case in.Kind == Int:
			out[in.Name] = 0
		default:
			out[in.Name] = false
		}
	}
	for k, val := range v {
		if _, ok := out[k]; !ok {
			out[k] = val
		}
	}
	return out
}

// Validate checks the inputs in set against v, in order, and returns the first
// problem, or nil when every rule passes. Inputs outside set are not checked,
// but rules may still read them (a Cond does), from v or their defaults.
//
// Values are taken as given: the wizard trims what the user typed first.
func Validate(set []*Input, v Values) *Problem {
	v = Resolve(v)
	for _, in := range set {
		for i, r := range in.AllRules() {
			if r.Applies(v) && !r.Passes(in, v) {
				return &Problem{Input: in, Rule: i, Message: r.Text(in, v)}
			}
		}
	}
	return nil
}

func str(v Values, name string) string {
	s, _ := v[name].(string)
	return s
}

// num reads an Int input. Callers pass ints; float64 is accepted too, so a
// fractional number (which another front end might take) can be checked.
func num(v Values, name string) float64 {
	switch n := v[name].(type) {
	case int:
		return float64(n)
	case float64:
		return n
	}
	return math.NaN()
}

// Lookup returns the input called name, or nil.
func Lookup(name string) *Input {
	for _, in := range All {
		if in.Name == name {
			return in
		}
	}
	return nil
}

// OnScreen returns the inputs a wizard step asks for, in order.
func OnScreen(s state.Step) []*Input {
	var out []*Input
	for _, in := range All {
		if in.Screen == s {
			out = append(out, in)
		}
	}
	return out
}

// ImagesFor is the image_source value a wizard run has chosen.
func ImagesFor(st *state.Setup) string {
	if st.Prebuilt() {
		return ImagesPrebuilt
	}
	return ImagesSource
}

// ImageVersion is the version an image tag names: the tag without any
// @digest, the cut snapshot makes.
func ImageVersion(tag string) string {
	v, _, _ := strings.Cut(tag, "@")
	return v
}
