// Package names implements systemd's naming rules for metrics sources and
// metric families, as systemd-report v260 enforces them.
//
// A source is a socket under /run/systemd/report/ whose file name is a
// valid Varlink interface name, such as io.systemd.Manager. A family name
// is the source name, a dot, and a Varlink field name, such as
// io.systemd.Manager.UnitsByTypeTotal. systemd-report drops families
// whose name does not start with the name of the socket they came from.
package names

import "strings"

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }

// ValidSource reports whether s is a valid Varlink interface name: dot
// separated, non-empty labels of letters, digits and single inner dashes,
// starting with a letter.
func ValidSource(s string) bool {
	if s == "" || !isLetter(s[0]) {
		return false
	}
	dot, dash := false, false
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '.':
			if dot || dash {
				return false
			}
			dot = true
		case c == '-':
			if dot || dash {
				return false
			}
			dash = true
		case isLetter(c) || isDigit(c):
			dot, dash = false, false
		default:
			return false
		}
	}
	return !dot && !dash
}

// ValidField reports whether s is a valid Varlink field name: a letter,
// then letters, digits and single underscores, not ending in an
// underscore.
func ValidField(s string) bool {
	if s == "" || !isLetter(s[0]) {
		return false
	}
	underscore := false
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '_':
			if underscore {
				return false
			}
			underscore = true
		case isLetter(c) || isDigit(c):
			underscore = false
		default:
			return false
		}
	}
	return !underscore
}

// ValidFamily reports whether name is a valid metric family name: an
// interface name, a dot, and a field name.
func ValidFamily(name string) bool {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return false
	}
	return ValidSource(name[:i]) && ValidField(name[i+1:])
}

// HasPrefix reports whether name starts with prefix followed by a dot.
// "foo" is not a prefix of "foo", but it is of "foo.bar".
func HasPrefix(name, prefix string) bool {
	return prefix != "" && len(name) > len(prefix)+1 && strings.HasPrefix(name, prefix) && name[len(prefix)] == '.'
}

// BelongsTo reports whether family is a valid family name that source may
// publish.
func BelongsTo(family, source string) bool {
	return ValidFamily(family) && HasPrefix(family, source)
}

// SanitizeField turns s into a valid field name: characters other than
// letters and digits become underscores, runs of underscores collapse,
// leading and trailing underscores are dropped, and a name that does not
// start with a letter gets an "m" prefix. An empty result becomes "m".
func SanitizeField(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 1)
	underscore := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isLetter(c) || isDigit(c) {
			if b.Len() == 0 && isDigit(c) {
				b.WriteByte('m')
			}
			b.WriteByte(c)
			underscore = false
			continue
		}
		if b.Len() > 0 && !underscore {
			b.WriteByte('_')
			underscore = true
		}
	}
	out := strings.TrimSuffix(b.String(), "_")
	if out == "" {
		return "m"
	}
	return out
}
