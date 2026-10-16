package provider_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/systemd/v260/metrics"
	"github.com/rossedman/sdvarlink/varlink"

	"github.com/rossedman/sdmetrics/provider"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestOptions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "io.example.Opts")
	g, err := user.LookupGroupId(strconv.Itoa(os.Getgid()))
	if err != nil {
		t.Skip("no group entry for the current gid")
	}
	p, err := provider.New("io.example.Opts",
		provider.WithSocketPath(path),
		provider.WithSocketOwner(os.Getuid(), -1),
		provider.WithSocketGroup(g.Name),
		provider.WithLogger(quiet),
		provider.WithIdleTimeout(time.Second),
		provider.WithMaxConnections(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "io.example.Opts" || p.Path() != path {
		t.Fatalf("Name/Path = %q %q", p.Name(), p.Path())
	}
	p.Gauge("Up").Set(1)
	c := serve(t, p)
	equal(t, list(t, c), []string{"io.example.Opts.Up 1"})
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	if int(st.Uid) != os.Getuid() || strconv.Itoa(int(st.Gid)) != g.Gid {
		t.Errorf("owner = %d:%d", st.Uid, st.Gid)
	}
	info, err := varlink.GetInfo(context.Background(), c.Caller())
	if err != nil || info.Vendor != "sdmetrics" || info.Product != "io.example.Opts" || info.Version == "" {
		t.Errorf("default server info = %+v, %v", info, err)
	}
}

func TestUnknownGroup(t *testing.T) {
	if _, err := provider.New("io.example.G", provider.WithSocketGroup("no-such-group-sdmetrics")); err == nil {
		t.Fatal("unknown group accepted")
	}
}

func TestBindErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "io.example.File")
	os.WriteFile(file, nil, 0o644)
	p, _ := provider.New("io.example.File", provider.WithDir(dir), provider.WithLogger(quiet))
	if err := p.Serve(context.Background()); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("Serve over a regular file: %v", err)
	}
}

func TestWithListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := provider.New("io.example.L", provider.WithListener(l), provider.WithLogger(quiet))
	p.Counter("C").Add(3)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Serve(ctx) }()
	c, err := metrics.DialPath(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	equal(t, list(t, c), []string{"io.example.L.C 3"})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestFromActivation re-executes the test binary the way systemd starts a
// socket-activated service: the listening socket as fd 3, LISTEN_FDS=1 and
// LISTEN_PID set to the child's own PID (by the shell's exec).
func TestFromActivation(t *testing.T) {
	if os.Getenv("SDMETRICS_ACTIVATION_CHILD") != "" {
		activationChild()
		return
	}
	dir, err := os.MkdirTemp("", "act")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "io.example.Act")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := l.File()
	if err != nil {
		t.Fatal(err)
	}
	for _, names := range []string{"io.example.Act", "queue.socket"} {
		t.Run(names, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", "-c", `LISTEN_PID=$$ exec "$0" -test.run '^TestFromActivation$'`, os.Args[0])
			cmd.Env = append(os.Environ(), "SDMETRICS_ACTIVATION_CHILD=1", "LISTEN_FDS=1", "LISTEN_FDNAMES="+names,
				"SDMETRICS_ACTIVATION_DIR="+filepath.Join(dir, "unused"))
			cmd.ExtraFiles = []*os.File{f}
			stdin, _ := cmd.StdinPipe()
			out := &strings.Builder{}
			cmd.Stdout, cmd.Stderr = out, out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				stdin.Close()
				cmd.Wait()
			}()
			var c *metrics.Client
			for i := 0; c == nil && i < 500; i++ {
				c, _ = metrics.DialPath(context.Background(), path)
				time.Sleep(10 * time.Millisecond)
			}
			if c == nil {
				t.Fatalf("activated child never answered:\n%s", out)
			}
			defer c.Close()
			equal(t, list(t, c), []string{"io.example.Act.Activated 1"})
			// The child bound nothing of its own.
			if _, err := os.Stat(filepath.Join(dir, "unused")); err == nil {
				t.Error("activated provider created its fallback directory")
			}
		})
	}
}

func activationChild() {
	p, err := provider.New("io.example.Act", provider.FromActivation(),
		provider.WithDir(os.Getenv("SDMETRICS_ACTIVATION_DIR")), provider.WithLogger(quiet))
	if err != nil {
		panic(err)
	}
	p.Gauge("Activated").Set(1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { io.Copy(io.Discard, os.Stdin); cancel() }() // the parent closes stdin to stop us
	if err := p.Serve(ctx); err != nil {
		panic(err)
	}
	os.Exit(0)
}

func TestFromActivationFallsBackToBinding(t *testing.T) {
	t.Setenv("LISTEN_PID", "")
	p := newProvider(t, "io.example.NotAct", provider.FromActivation(), provider.WithLogger(quiet))
	p.Gauge("Up").Set(1)
	c := serve(t, p) // dials p.Path(), the bound socket
	equal(t, list(t, c), []string{"io.example.NotAct.Up 1"})
}

type valuesCollector struct{}

func (valuesCollector) Collect(context.Context) ([]provider.Family, error) {
	var samples []provider.Sample
	for i, v := range []any{int(1), int8(2), int16(3), int32(4), int64(5), uint(6), uint8(7), uint16(8), uint32(9), uint64(10), float32(0.5), 1.25} {
		samples = append(samples, provider.Sample{Object: strconv.Itoa(i), Value: v})
	}
	return []provider.Family{{Name: "N", Type: provider.TypeGauge, Samples: samples}}, nil
}

func TestCollectorValueKinds(t *testing.T) {
	p := newProvider(t, "io.example.V", provider.WithLogger(quiet))
	p.Register(valuesCollector{})
	got := list(t, serve(t, p))
	if len(got) != 12 || !strings.Contains(strings.Join(got, "\n"), "{11} 1.25") || !strings.Contains(strings.Join(got, "\n"), "{10} 0.5") {
		t.Fatalf("values: %v", got)
	}
}

type failingCollector struct{}

func (failingCollector) Collect(context.Context) ([]provider.Family, error) {
	return []provider.Family{{Name: "Partial", Type: provider.TypeGauge, Samples: []provider.Sample{{Value: 1}}}}, io.ErrUnexpectedEOF
}

func TestCollectorErrorKeepsPartialResults(t *testing.T) {
	p := newProvider(t, "io.example.F", provider.WithLogger(quiet))
	p.Register(failingCollector{})
	equal(t, list(t, serve(t, p)), []string{"io.example.F.Partial 1"})
}

func TestHandles(t *testing.T) {
	p := newProvider(t, "io.example.H", provider.WithLogger(quiet))
	state := p.String("State", provider.Labels("kind"))
	ctr := p.Counter("Restarts")
	g := p.Gauge("Plain")
	c := serve(t, p)

	s := state.On("unit-a").With("svc")
	s.Set("running")
	if s.Value() != "running" || state.With("other").Value() != "" {
		t.Errorf("String values: %q %q", s.Value(), state.With("other").Value())
	}
	ctr.On("unit-a").Add(2)
	ctr.Add(1)
	g.Set(5)
	equal(t, list(t, c), []string{
		`io.example.H.Plain 5`,
		`io.example.H.Restarts 1`,
		`io.example.H.Restarts{unit-a} 2`,
		`io.example.H.State{unit-a}{"kind":"svc"} "running"`,
	})
	s.Delete()
	ctr.On("unit-a").Delete()
	ctr.Delete() // root: reset, still published
	g.Delete()
	equal(t, list(t, c), []string{`io.example.H.Plain 0`, `io.example.H.Restarts 0`})
}
