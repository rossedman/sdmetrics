package scrape

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnitFromCgroupPath(t *testing.T) {
	for path, want := range map[string]string{
		"/system.slice/systemd-networkd.service": "systemd-networkd.service",
		"/init.scope":                            "init.scope",
		"/system.slice/foo.service/worker":       "foo.service",
		"/user.slice/user-1000.slice/user@1000.service/app.slice/foo.service":          "foo.service",
		"/user.slice/user-1000.slice/user@1000.service/init.scope":                     "init.scope",
		"/user.slice/user-1000.slice/session-2.scope":                                  "session-2.scope",
		"/machine.slice/systemd-nspawn@web.service/payload/system.slice/nginx.service": "nginx.service",
		"/system.slice":  "system.slice",
		"/":              "",
		"":               "",
		"/docker/abc123": "",
		"/.service":      "",
	} {
		if got := UnitFromCgroupPath(path); got != want {
			t.Errorf("UnitFromCgroupPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestUnitFromProcCgroup(t *testing.T) {
	for content, want := range map[string]string{
		"0::/system.slice/a.service\n": "a.service",
		"12:cpu:/x\n1:name=systemd:/system.slice/v1.service\n0::/system.slice/v2.service\n": "v2.service",
		"12:cpu,cpuacct:/\n1:name=systemd:/system.slice/v1.service\n":                       "v1.service",
		"garbage\n": "",
		"":          "",
	} {
		if got := UnitFromProcCgroup(content); got != want {
			t.Errorf("UnitFromProcCgroup(%q) = %q, want %q", content, got, want)
		}
	}
}

func TestResolveUnitActivated(t *testing.T) {
	proc := t.TempDir()
	os.MkdirAll(filepath.Join(proc, "1"), 0o755)
	os.WriteFile(filepath.Join(proc, "1", "cgroup"), []byte("0::/init.scope\n"), 0o644)
	if u, err := resolveUnit(proc, 1, "io.systemd.Manager"); err != nil || u != "init.scope" {
		t.Errorf("manager: %q, %v", u, err)
	}
	if _, err := resolveUnit(proc, 1, "io.example.Queue"); !errors.Is(err, ErrActivated) {
		t.Errorf("activated: %v", err)
	}
	if _, err := resolveUnit(proc, 0, "io.example.Queue"); err == nil {
		t.Error("pid 0 resolved")
	}
}

func TestDecodeValue(t *testing.T) {
	for raw, want := range map[string]Value{
		`42`:                   {Kind: KindInt, Int: 42},
		`-3`:                   {Kind: KindInt, Int: -3},
		`1.5`:                  {Kind: KindFloat, Float: 1.5},
		`18446744073709551615`: {Kind: KindFloat, Float: 18446744073709551615},
		`"up"`:                 {Kind: KindString, Str: "up"},
	} {
		got, err := decodeValue([]byte(raw))
		if err != nil || got != want {
			t.Errorf("decodeValue(%s) = %+v, %v", raw, got, err)
		}
	}
	for _, raw := range []string{``, `true`, `null`, `{}`, `[1]`, `1e999`, `"unterminated`} {
		if _, err := decodeValue([]byte(raw)); err == nil {
			t.Errorf("decodeValue(%s) succeeded", raw)
		}
	}
}

func TestDecodeFields(t *testing.T) {
	got, err := decodeFields([]byte(`{"state":"active","n":3}`))
	if err != nil || got["state"] != "active" || got["n"] != "3" {
		t.Fatalf("decodeFields = %v, %v", got, err)
	}
	if got, err := decodeFields(nil); got != nil || err != nil {
		t.Errorf("nil fields = %v, %v", got, err)
	}
	if _, err := decodeFields([]byte(`[1]`)); err == nil {
		t.Error("array fields accepted")
	}
}

func FuzzDecodeValue(f *testing.F) {
	for _, s := range []string{"1", "-1", "1.5", `"x"`, "1e400", "{}", "18446744073709551616"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		v, err := decodeValue(b)
		if err == nil && v.Kind != KindInt && v.Kind != KindFloat && v.Kind != KindString {
			t.Fatalf("decodeValue(%q) = %+v with no error", b, v)
		}
	})
}

func TestValueString(t *testing.T) {
	for v, want := range map[Value]string{
		{Kind: KindInt, Int: -2}:     "-2",
		{Kind: KindFloat, Float: .5}: "0.5",
		{Kind: KindString, Str: "a"}: `"a"`,
		{}:                           "<invalid>",
	} {
		if got := v.String(); got != want {
			t.Errorf("%+v.String() = %q, want %q", v, got, want)
		}
	}
	if (Value{Kind: KindFloat, Float: 2.5}).Number() != 2.5 || (Value{Kind: KindInt, Int: 3}).Number() != 3 {
		t.Error("Number")
	}
}
