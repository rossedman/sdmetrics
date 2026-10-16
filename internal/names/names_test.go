package names

import "testing"

func TestValidSource(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"io.systemd.Manager", true},
		{"io.example.queue", true},
		{"io.example.my-queue", true},
		{"io", true},
		{"a1.b2", true},
		{"", false},
		{"1io.systemd", false},
		{".io", false},
		{"io.", false},
		{"io..systemd", false},
		{"io.-x", false},
		{"io.x-", false},
		{"io.x--y", false},
		{"io.my_queue", false},
		{".io.systemd.Manager.lock", false},
		{"io/systemd", false},
	} {
		if got := ValidSource(tc.in); got != tc.want {
			t.Errorf("ValidSource(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidField(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"UnitsByTypeTotal", true},
		{"queue_depth", true},
		{"a", true},
		{"a1_b2", true},
		{"", false},
		{"_a", false},
		{"1a", false},
		{"a__b", false},
		{"a_", false},
		{"a.b", false},
		{"a-b", false},
	} {
		if got := ValidField(tc.in); got != tc.want {
			t.Errorf("ValidField(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFamilies(t *testing.T) {
	for _, tc := range []struct {
		family, source string
		valid, belongs bool
	}{
		{"io.systemd.Manager.UnitsByTypeTotal", "io.systemd.Manager", true, true},
		{"io.example.queue.queue_depth", "io.example.queue", true, true},
		{"io.example.queue.sub.depth", "io.example.queue", true, true},
		{"io.example.queue.depth", "io.example.other", true, false},
		{"io.example.queuex.depth", "io.example.queue", true, false},
		{"io.example.queue", "io.example.queue", true, false},
		{"depth", "io.example.queue", false, false},
		{"io.example.queue.bad__name", "io.example.queue", false, false},
		{"io.example.my_q.depth", "io.example.my_q", false, false},
	} {
		if got := ValidFamily(tc.family); got != tc.valid {
			t.Errorf("ValidFamily(%q) = %v, want %v", tc.family, got, tc.valid)
		}
		if got := BelongsTo(tc.family, tc.source); got != tc.belongs {
			t.Errorf("BelongsTo(%q, %q) = %v, want %v", tc.family, tc.source, got, tc.belongs)
		}
	}
}

func TestSanitizeField(t *testing.T) {
	for in, want := range map[string]string{
		"http_requests_total":          "http_requests_total",
		"http.server.request.duration": "http_server_request_duration",
		"go:gc__duration":              "go_gc_duration",
		"_leading":                     "leading",
		"trailing_":                    "trailing",
		"9lives":                       "m9lives",
		"":                             "m",
		"___":                          "m",
		"ünïcode":                      "n_code",
	} {
		got := SanitizeField(in)
		if got != want {
			t.Errorf("SanitizeField(%q) = %q, want %q", in, got, want)
		}
		if !ValidField(got) {
			t.Errorf("SanitizeField(%q) = %q is not a valid field", in, got)
		}
	}
}

func FuzzSanitizeField(f *testing.F) {
	for _, s := range []string{"a", "a__b", "1", "x.y-z", "__"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got := SanitizeField(s); !ValidField(got) {
			t.Fatalf("SanitizeField(%q) = %q, not a valid field", s, got)
		}
	})
}
