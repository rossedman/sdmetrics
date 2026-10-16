//go:build integration

// Package integration runs sdmetrics against real systemd. Run it as root
// on a host, VM or container booted with systemd v260 or newer (CI boots
// one per distribution):
//
//	CGO_ENABLED=0 go build -o /tmp/queued ./examples/queued
//	SDMETRICS_QUEUED=/tmp/queued go test -tags integration ./integration/
//
// Tests that start units need root and SDMETRICS_QUEUED, and clean up the
// transient units they create. Nothing else on the host is changed.
package integration

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"

	"github.com/rossedman/sdmetrics/internal/exporter"
	"github.com/rossedman/sdmetrics/scrape"
)

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

func needReportDir(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(scrape.DefaultDir); err != nil {
		t.Skipf("%s missing: systemd v260+ required", scrape.DefaultDir)
	}
}

func needRoot(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	bin := os.Getenv("SDMETRICS_QUEUED")
	if bin == "" {
		t.Skip("SDMETRICS_QUEUED not set")
	}
	return bin
}

func scrapeAll(t *testing.T) map[string]scrape.Source {
	t.Helper()
	res, err := scrape.New(scrape.Options{}).Scrape(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]scrape.Source{}
	for _, s := range res.Sources {
		out[s.Name] = s
		t.Logf("%s unit=%q err=%v families=%d dropped=%d", s.Name, s.Unit, s.Err, len(s.Families), s.Invalid+s.Untyped)
	}
	return out
}

func systemdReport(t *testing.T, args ...string) string {
	t.Helper()
	bin, err := exec.LookPath("systemd-report")
	if err != nil {
		for _, p := range []string{"/usr/lib/systemd/systemd-report", "/lib/systemd/systemd-report"} {
			if _, err := os.Stat(p); err == nil {
				bin = p
			}
		}
	}
	if bin == "" {
		t.Skip("systemd-report not installed")
	}
	out, err := exec.CommandContext(ctx(t), bin, append([]string{"--no-pager"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("systemd-report %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestSystemdSources is the v0.1 gate: systemd's own sources scrape with
// correct types and identity.
func TestSystemdSources(t *testing.T) {
	needReportDir(t)
	srcs := scrapeAll(t)
	m, ok := srcs["io.systemd.Manager"]
	if !ok {
		t.Fatal("io.systemd.Manager not found")
	}
	if m.Err != nil {
		t.Fatal(m.Err)
	}
	if m.Unit != "init.scope" || m.Peer == nil || m.Peer.PID != 1 {
		t.Errorf("manager identity: unit=%q peer=%+v err=%v", m.Unit, m.Peer, m.UnitErr)
	}
	if m.Invalid+m.Untyped+m.Truncated > 0 {
		t.Errorf("manager values dropped: invalid=%d untyped=%d truncated=%d", m.Invalid, m.Untyped, m.Truncated)
	}
	// From v262, systemd socket-activates its other sources; they resolve
	// through their socket units.
	if n, ok := srcs["io.systemd.Network"]; ok && n.Activated && n.Unit != "" && n.Unit != "systemd-networkd.service" {
		t.Errorf("io.systemd.Network unit = %q", n.Unit)
	}
	types := map[scrape.Type]bool{}
	for _, f := range m.Families {
		types[f.Type] = true
		if !strings.HasPrefix(f.Name, "io.systemd.Manager.") {
			t.Errorf("family %s", f.Name)
		}
	}
	for _, want := range []scrape.Type{scrape.TypeCounter, scrape.TypeGauge, scrape.TypeString} {
		if !types[want] {
			t.Errorf("no %s family from the manager", want)
		}
	}
}

func waitSocket(t *testing.T, path string) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if c, err := net.Dial("unix", path); err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never accepted connections", path)
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.CommandContext(ctx(t), name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// TestSampleDaemon is the v0.2 gate: the SDK sample daemon, running as a
// service, shows up in both systemd-report and the scraper, attributed to
// its unit.
func TestSampleDaemon(t *testing.T) {
	needReportDir(t)
	bin := needRoot(t)
	const unit = "sdmetrics-queued-test.service"
	run(t, "systemd-run", "--unit", unit, "--property=Type=exec", bin)
	t.Cleanup(func() { exec.Command("systemctl", "stop", unit).Run() })
	waitSocket(t, filepath.Join(scrape.DefaultDir, "io.example.Queue"))

	q, ok := scrapeAll(t)["io.example.Queue"]
	if !ok || q.Err != nil {
		t.Fatalf("io.example.Queue: %+v", q)
	}
	if q.Unit != unit {
		t.Errorf("unit = %q (%v), want %s", q.Unit, q.UnitErr, unit)
	}
	if len(q.Families) < 4 {
		t.Errorf("families = %d", len(q.Families))
	}
	out := systemdReport(t, "metrics", "io.example.Queue")
	if !strings.Contains(out, "io.example.Queue.Depth") || !strings.Contains(out, "io.example.Queue.WorkerState") {
		t.Errorf("systemd-report output:\n%s", out)
	}
	// The verb is describe-metrics in v260 and describe from v262.
	verb := "describe"
	if strings.Contains(systemdReport(t, "--help"), "describe-metrics") {
		verb = "describe-metrics"
	}
	desc := systemdReport(t, verb, "io.example.Queue")
	if !strings.Contains(desc, "Jobs waiting") {
		t.Errorf("systemd-report describe-metrics output:\n%s", desc)
	}
}

// TestSocketActivated runs the sample daemon behind a .socket unit. Both
// systemd-report and the scraper read it. The socket's peer credentials
// are PID 1's, so the scraper asks the manager which unit the socket
// triggers; systemd v262 and newer say, older releases leave the unit
// unresolved.
func TestSocketActivated(t *testing.T) {
	needReportDir(t)
	bin := needRoot(t)
	// queued always publishes io.example.Queue; systemd binds that socket
	// here and hands it to queued, which adopts it with FromActivation.
	dir := "/run/systemd/system"
	units := map[string]string{
		"sdmetrics-activated.socket":  "[Socket]\nListenStream=" + filepath.Join(scrape.DefaultDir, "io.example.Queue") + "\nSocketMode=0666\nRemoveOnStop=yes\n",
		"sdmetrics-activated.service": "[Service]\nExecStart=" + bin + "\n",
	}
	exec.Command("systemctl", "stop", "sdmetrics-queued-test.service").Run()
	for name, body := range units {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(filepath.Join(dir, name)) })
	}
	run(t, "systemctl", "daemon-reload")
	run(t, "systemctl", "start", "sdmetrics-activated.socket")
	t.Cleanup(func() {
		exec.Command("systemctl", "stop", "sdmetrics-activated.socket", "sdmetrics-activated.service").Run()
		exec.Command("systemctl", "daemon-reload").Run()
	})

	q, ok := scrapeAll(t)["io.example.Queue"]
	if !ok || q.Err != nil {
		t.Fatalf("io.example.Queue: %+v", q)
	}
	if !q.Activated {
		t.Errorf("source not marked activated: %+v", q)
	}
	if q.Unit != "sdmetrics-activated.service" && !errors.Is(q.UnitErr, scrape.ErrActivated) {
		t.Errorf("activated source: unit=%q err=%v, want sdmetrics-activated.service or ErrActivated", q.Unit, q.UnitErr)
	}
	if !strings.Contains(systemdReport(t, "metrics", "io.example.Queue"), "io.example.Queue.Depth") {
		t.Error("systemd-report cannot read the activated provider")
	}
}

type sink struct {
	colmetrics.UnimplementedMetricsServiceServer
	mu   sync.Mutex
	reqs []*colmetrics.ExportMetricsServiceRequest
}

func (s *sink) Export(_ context.Context, r *colmetrics.ExportMetricsServiceRequest) (*colmetrics.ExportMetricsServiceResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r)
	return &colmetrics.ExportMetricsServiceResponse{}, nil
}

// TestExporterToOTLP scrapes the host end to end into an in-memory OTLP
// sink.
func TestExporterToOTLP(t *testing.T) {
	needReportDir(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	srv := grpc.NewServer()
	colmetrics.RegisterMetricsServiceServer(srv, s)
	go srv.Serve(l)
	defer srv.Stop()

	cfg := exporter.Default()
	cfg.Interval = time.Hour
	cfg.OTLP.Endpoint = l.Addr().String()
	cfg.OTLP.Insecure = true
	c, cancel := context.WithCancel(ctx(t))
	e, err := exporter.New(c, cfg, slog.Default(), "integration")
	if err != nil {
		t.Fatal(err)
	}
	go e.Run(c)
	// One export per resource: wait until the manager's arrives.
	seen := func() map[string]bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		units := map[string]bool{}
		for _, r := range s.reqs {
			for _, rm := range r.ResourceMetrics {
				for _, kv := range rm.Resource.Attributes {
					if kv.Key == "systemd.unit.name" {
						units[kv.Value.GetStringValue()] = true
					}
				}
			}
		}
		return units
	}
	deadline := time.Now().Add(20 * time.Second)
	for !seen()["init.scope"] && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	if units := seen(); !units["init.scope"] {
		t.Fatalf("no resource for init.scope; units: %v", units)
	}
}
