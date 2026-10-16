package otelmap

import "strings"

// unitSuffixes map a trailing name word to an OpenTelemetry (UCUM) unit.
var unitSuffixes = []struct{ word, unit string }{
	{"seconds", "s"},
	{"milliseconds", "ms"},
	{"microseconds", "us"},
	{"nanoseconds", "ns"},
	{"bytes", "By"},
	{"ratio", "1"},
	{"percent", "%"},
}

// OTelName converts a systemd family name to an OpenTelemetry-style name
// and unit; see NamingOTel.
func OTelName(family string) (name, unit string) {
	ns, field := "", family
	if i := strings.LastIndexByte(family, '.'); i >= 0 {
		ns, field = family[:i], family[i+1:]
	}
	ns = strings.ToLower(ns)
	if ns == "io.systemd" || strings.HasPrefix(ns, "io.systemd.") {
		ns = strings.TrimPrefix(ns, "io.")
	}
	words := strings.Split(snake(field), "_")
	if len(words) > 1 && words[len(words)-1] == "total" {
		words = words[:len(words)-1]
	}
	if len(words) > 1 {
		last := words[len(words)-1]
		for _, s := range unitSuffixes {
			if last == s.word {
				unit = s.unit
				words = words[:len(words)-1]
				break
			}
		}
	}
	name = strings.Join(words, "_")
	if ns != "" {
		name = strings.ReplaceAll(ns, "-", "_") + "." + name
	}
	return name, unit
}

// snake converts CamelCase or mixed names to snake_case, keeping
// acronyms together: NRestarts -> n_restarts, IPv4AddressState ->
// ipv4_address_state, queue_depth -> queue_depth.
func snake(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	isUpper := func(c byte) bool { return c >= 'A' && c <= 'Z' }
	isLower := func(c byte) bool { return c >= 'a' && c <= 'z' }
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_')
			}
			continue
		}
		if isUpper(c) && i > 0 {
			prev := s[i-1]
			// An acronym ends before the capital that starts a word: the S in
			// HTTPServer. A single lower-case letter does not start a word, so
			// IPv4 stays together.
			lowerRun := 0
			for j := i + 1; j < len(s) && isLower(s[j]); j++ {
				lowerRun++
			}
			if isLower(prev) || isDigit(prev) || (isUpper(prev) && lowerRun >= 2) {
				if !strings.HasSuffix(b.String(), "_") {
					b.WriteByte('_')
				}
			}
		}
		if isUpper(c) {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return strings.Trim(b.String(), "_")
}
