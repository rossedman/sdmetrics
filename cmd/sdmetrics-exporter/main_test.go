package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/sdmetrics/provider"
)

func TestVersion(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-version"}, &out, io.Discard); code != 0 || out.Len() == 0 {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
}

func TestBadUsage(t *testing.T) {
	for _, args := range [][]string{
		{"-nope"},
		{"extra"},
		{"-naming", "fancy", "-once"},
		{"-log.level", "loud"},
		{"-config", "/nonexistent.yaml"},
	} {
		if code := run(args, io.Discard, io.Discard); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestOnce(t *testing.T) {
	dir, err := os.MkdirTemp("", "report")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	p, _ := provider.New("io.example.Cli", provider.WithDir(dir), provider.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	p.Gauge("Up").Set(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Serve(ctx)
	for i := 0; i < 1000; i++ {
		if c, err := net.Dial("unix", p.Path()); err == nil {
			c.Close()
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	var out bytes.Buffer
	if code := run([]string{"-once", "-dir", dir}, &out, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "io.example.Cli.Up") {
		t.Errorf("text output:\n%s", out.String())
	}
	out.Reset()
	if code := run([]string{"-once", "-dir", dir, "-format", "json", "-include", "io.example.*"}, &out, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), `"name": "io.example.Cli.Up"`) {
		t.Errorf("json output:\n%s", out.String())
	}
	if code := run([]string{"-once", "-dir", filepath.Join(dir, "missing")}, io.Discard, io.Discard); code != 1 {
		t.Errorf("missing dir: exit %d, want 1", code)
	}
}

func TestOldSystemdExitsCleanly(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if code := run([]string{"-dir", missing, "-prometheus.listen", "127.0.0.1:0"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
}

func TestFlagsOverrideConfig(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(cfg, []byte("interval: 1m\nnaming: raw\n"), 0o644)
	missing := filepath.Join(t.TempDir(), "missing")
	var errOut bytes.Buffer
	code := run([]string{
		"-config", cfg, "-dir", missing,
		"-interval", "5s", "-timeout", "1s", "-concurrency", "2", "-naming", "otel",
		"-include", "io.systemd.*,io.example.*", "-exclude", "io.example.B",
		"-otlp.endpoint", "127.0.0.1:1", "-otlp.protocol", "grpc", "-otlp.insecure",
		"-prometheus.listen", "127.0.0.1:0",
		"-log.format", "json", "-log.level", "debug",
	}, io.Discard, &errOut)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut.String())
	}
	for _, want := range []string{`"interval":"5s"`, `"otlp":true`, `"msg":"no report directory`} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("log lacks %s:\n%s", want, errOut.String())
		}
	}
}

func TestFailures(t *testing.T) {
	dir := t.TempDir()
	for name, args := range map[string][]string{
		"invalid config":   {"-timeout", "1m", "-interval", "1s"},
		"nothing to do":    {"-dir", dir},
		"bad log format":   {"-log.format", "xml"},
		"bad -once format": {"-once", "-dir", dir, "-format", "yaml"},
	} {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		code := run(args, io.Discard, io.Discard)
		want := 2
		if name == "bad -once format" {
			want = 1
		}
		if code != want {
			t.Errorf("%s: exit %d, want %d", name, code, want)
		}
	}
	// The exporter stops with an error when its endpoint cannot listen.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	if code := run([]string{"-dir", dir, "-prometheus.listen", l.Addr().String()}, io.Discard, io.Discard); code != 1 {
		t.Errorf("busy port: exit %d, want 1", code)
	}
	if code := run([]string{"-help"}, io.Discard, io.Discard); code != 0 {
		t.Errorf("-help: exit %d", code)
	}
}

func TestVersionString(t *testing.T) {
	defer func(v, c, d string) { version, commit, date = v, c, d }(version, commit, date)
	version, commit, date = "1.2.3", "abc1234", "2026-10-16T12:00:00Z"
	if got := versionString(); got != "1.2.3 (abc1234, 2026-10-16)" {
		t.Errorf("versionString = %q", got)
	}
	version, commit, date = "1.2.3", "", ""
	if got := versionString(); got != "1.2.3" {
		t.Errorf("versionString = %q", got)
	}
}
