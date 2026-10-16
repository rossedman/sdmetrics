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
