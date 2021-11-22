package header

import (
	"fmt"
	"net/http"
	"net/textproto"
	"regexp"
	"strings"
)

// ValueMatcher comes in two flavors. One that performs an exact match against a string and one that
// uses a regular expression.
type ValueMatcher interface {
	fmt.Stringer

	// Matches returns true if the given string matches this ValueMatcher
	Matches(value string) bool

	// Op returns either == or =~.
	Op() string
}

// Matcher uses a set of ValueMatchers to match a http.Header
type Matcher interface {
	// Map returns the map correspondence of this Matcher. The returned value can be
	// used as an argument to NewMatcher to create an identical Matcher.
	Map() map[string]string

	// Matches returns true if all ValueMatchers in this Matcher are matched by the given http.Header.
	// Header name comparison is made using the textproto.CanonicalMIMEHeaderKey form of the keys.
	Matches(header http.Header) bool
}

type textMatcher string

func (t textMatcher) Matches(value string) bool {
	return string(t) == value
}

func (t textMatcher) String() string {
	return string(t)
}

func (t textMatcher) Op() string {
	return "=="
}

type rxMatcher struct {
	*regexp.Regexp
}

func (r rxMatcher) Matches(value string) bool {
	return value != "" && r.MatchString(value)
}

func (r rxMatcher) Op() string {
	return "=~"
}

// NewValueMatcher returns a ValueMatcher that is either an exact text matcher or a regexp matcher. A
// string that has no regexp-meta characters or that cannot be compiled into a regexp will yield an
// exact text matcher.
func NewValueMatcher(v string) ValueMatcher {
	if regexp.QuoteMeta(v) == v {
		return textMatcher(v)
	}
	rx, err := regexp.Compile(v)
	if err != nil {
		return textMatcher(v)
	}
	return rxMatcher{rx}
}

type matcher map[string]ValueMatcher

// NewMatcher creates a new Matcher. The given match results in a set of ValueMatcher instances.
func NewMatcher(hs map[string]string) Matcher {
	hm := make(matcher, len(hs))
	for k, v := range hs {
		hm[textproto.CanonicalMIMEHeaderKey(k)] = NewValueMatcher(v)
	}
	return hm
}

func (m matcher) Map() map[string]string {
	r := make(map[string]string, len(m))
	for k, v := range m {
		r[k] = v.String()
	}
	return r
}

// Matches returns true if all ValueMatchers in this Matcher are matched by the given http.Header.
// Header name comparison is made using the textproto.CanonicalMIMEHeaderKey form of the keys.
func (m matcher) Matches(h http.Header) bool {
	for name, vm := range m {
		if v := h.Get(name); !vm.Matches(v) {
			return false
		}
	}
	return true
}

func (m matcher) String() string {
	if len(m) == 0 {
		return "match=all"
	}
	sb := strings.Builder{}
	for k, v := range m {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		fmt.Fprintf(&sb, "match=%s%s%s", k, v.Op(), v)
	}
	return sb.String()
}
