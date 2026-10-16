package otelmap

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/rossedman/sdmetrics/scrape"
)

func TestOTelName(t *testing.T) {
	for in, want := range map[string][2]string{
		"io.systemd.Manager.UnitsByTypeTotal":   {"systemd.manager.units_by_type", ""},
		"io.systemd.Manager.NRestarts":          {"systemd.manager.n_restarts", ""},
		"io.systemd.Network.IPv4AddressState":   {"systemd.network.ipv4_address_state", ""},
		"io.systemd.Network.ManagedInterfaces":  {"systemd.network.managed_interfaces", ""},
		"io.example.Api.RequestLatencySeconds":  {"io.example.api.request_latency", "s"},
		"io.example.Api.ResponseBytesTotal":     {"io.example.api.response", "By"},
		"io.example.Api.ResponseBytes":          {"io.example.api.response", "By"},
		"io.example.queue.queue_depth":          {"io.example.queue.queue_depth", ""},
		"io.example.queue.jobs_processed_total": {"io.example.queue.jobs_processed", ""},
		"io.example.my-app.CPUUsageRatio":       {"io.example.my_app.cpu_usage", "1"},
		"io.example.A.Seconds":                  {"io.example.a.seconds", ""},
		"io.example.A.HTTPServer":               {"io.example.a.http_server", ""},
	} {
		name, unit := OTelName(in)
		if name != want[0] || unit != want[1] {
			t.Errorf("OTelName(%q) = %q, %q; want %q, %q", in, name, unit, want[0], want[1])
		}
	}
}

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func result(at time.Time, srcs ...scrape.Source) *scrape.Result {
	return &scrape.Result{Time: at, Duration: 10 * time.Millisecond, Sources: srcs}
}

func intV(n int64) scrape.Value     { return scrape.Value{Kind: scrape.KindInt, Int: n} }
func floatV(f float64) scrape.Value { return scrape.Value{Kind: scrape.KindFloat, Float: f} }

func manager(restarts int64, pid int32) scrape.Source {
	return scrape.Source{
		Name: "io.systemd.Manager", Unit: "init.scope", Peer: &scrape.Peer{PID: pid},
		Duration: 3 * time.Millisecond,
		Families: []scrape.Family{
			{Name: "io.systemd.Manager.NRestarts", Description: "Per unit metric: number of restarts", Type: scrape.TypeCounter,
				Metrics: []scrape.Metric{{Object: "a.service", Value: intV(restarts)}}},
			{Name: "io.systemd.Manager.UnitActiveState", Type: scrape.TypeString,
				Metrics: []scrape.Metric{{Object: "a.service", Value: scrape.Value{Kind: scrape.KindString, Str: "active"}}}},
			{Name: "io.systemd.Manager.UnitsByTypeTotal", Type: scrape.TypeGauge,
				Metrics: []scrape.Metric{{Fields: map[string]string{"type": "service"}, Value: intV(40)}, {Fields: map[string]string{"type": "mount"}, Value: floatV(2.5)}}},
			{Name: "io.systemd.Manager.Empty", Type: scrape.TypeGauge},
		},
	}
}

func attrs(as []Attribute) string {
	var b []string
	for _, a := range as {
		b = append(b, a.Key+"="+a.String())
	}
	return strings.Join(b, ",")
}

func find(b Batch, source, metric string) *Metric {
	for i := range b.Resources {
		if b.Resources[i].Source != source {
			continue
		}
		for j := range b.Resources[i].Metrics {
			if b.Resources[i].Metrics[j].Name == metric {
				return &b.Resources[i].Metrics[j]
			}
		}
	}
	return nil
}

func TestMap(t *testing.T) {
	m := NewMapper(Options{HostName: "h1", HostID: "abc", Resource: map[string]string{"deployment.environment": "prod"}})
	b := m.Map(result(t0, manager(2, 1)))

	if len(b.Resources) != 2 || b.Resources[0].Source != "io.systemd.Manager" || b.Resources[1].Source != "" {
		t.Fatalf("resources = %+v", b.Resources)
	}
	r := b.Resources[0]
	if got, want := attrs(r.Attributes), "host.name=h1,host.id=abc,deployment.environment=prod,systemd.report.source=io.systemd.Manager,systemd.unit.name=init.scope,process.pid=1,service.name=systemd"; got != want {
		t.Errorf("resource attributes:\n got %s\nwant %s", got, want)
	}
	if len(r.Metrics) != 3 {
		t.Fatalf("metrics = %d, want 3 (empty family skipped)", len(r.Metrics))
	}

	nr := find(b, "io.systemd.Manager", "io.systemd.Manager.NRestarts")
	if nr.Kind != Sum || !nr.Monotonic || nr.Description == "" {
		t.Errorf("NRestarts = %+v", nr)
	}
	p := nr.Points[0]
	if !p.IsInt || p.Int != 2 || !p.Start.Equal(p.Time) || attrs(p.Attributes) != "systemd.object=a.service" {
		t.Errorf("NRestarts point = %+v", p)
	}

	st := find(b, "io.systemd.Manager", "io.systemd.Manager.UnitActiveState")
	if st.Kind != Gauge || st.Points[0].Int != 1 || attrs(st.Points[0].Attributes) != "systemd.object=a.service,systemd.value=active" {
		t.Errorf("UnitActiveState = %+v", st)
	}

	g := find(b, "io.systemd.Manager", "io.systemd.Manager.UnitsByTypeTotal")
	if g.Kind != Gauge || g.Points[0].Int != 40 || g.Points[1].IsInt || g.Points[1].Float != 2.5 || attrs(g.Points[0].Attributes) != "type=service" {
		t.Errorf("UnitsByTypeTotal = %+v", g)
	}
}

func TestCounterStartTimes(t *testing.T) {
	m := NewMapper(Options{HostName: "h", HostID: "i"})
	first := find(m.Map(result(t0, manager(2, 1))), "io.systemd.Manager", "io.systemd.Manager.NRestarts").Points[0]
	t1 := t0.Add(30 * time.Second)
	second := find(m.Map(result(t1, manager(5, 1))), "io.systemd.Manager", "io.systemd.Manager.NRestarts").Points[0]
	if !second.Start.Equal(first.Start) || second.Time.Equal(second.Start) {
		t.Errorf("start moved without a reset: %v -> %v", first.Start, second.Start)
	}
	// A decrease is a reset.
	t2 := t1.Add(30 * time.Second)
	reset := find(m.Map(result(t2, manager(1, 1))), "io.systemd.Manager", "io.systemd.Manager.NRestarts").Points[0]
	if !reset.Start.Equal(reset.Time) {
		t.Errorf("decrease did not reset start: %+v", reset)
	}
	// So is a new process behind the socket, even if the value grew.
	t3 := t2.Add(30 * time.Second)
	restarted := find(m.Map(result(t3, manager(9, 42))), "io.systemd.Manager", "io.systemd.Manager.NRestarts").Points[0]
	if !restarted.Start.Equal(restarted.Time) {
		t.Errorf("new PID did not reset start: %+v", restarted)
	}
	// Start times survive a few missed scrapes, then expire.
	for i := 0; i < startTTL+2; i++ {
		m.Map(result(t3.Add(time.Duration(i+1) * time.Second)))
	}
	if len(m.starts) != 0 {
		t.Errorf("stale start times kept: %d", len(m.starts))
	}
}

func TestServiceNames(t *testing.T) {
	m := NewMapper(Options{HostName: "h", HostID: "i",
		ServiceNames: map[string]string{"io.example.Over": "custom"},
		Units:        map[string]string{"io.example.Act": "queue.service"}})
	for _, tc := range []struct {
		src  scrape.Source
		want string
	}{
		{scrape.Source{Name: "io.systemd.Network", Unit: "systemd-networkd.service", Peer: &scrape.Peer{PID: 7}}, "systemd.unit.name=systemd-networkd.service,process.pid=7,service.name=systemd-networkd"},
		{scrape.Source{Name: "io.example.Getty", Unit: "getty@tty1.service"}, "systemd.unit.name=getty@tty1.service,service.name=getty"},
		{scrape.Source{Name: "io.example.Scope", Unit: "session-2.scope"}, "systemd.unit.name=session-2.scope,service.name=session-2.scope"},
		{scrape.Source{Name: "io.example.Over", Unit: "x.service"}, "systemd.unit.name=x.service,service.name=custom"},
		{scrape.Source{Name: "io.example.Unknown", Peer: &scrape.Peer{PID: 1}, UnitErr: scrape.ErrActivated}, "service.name=io.example.Unknown"},
		{scrape.Source{Name: "io.example.Act", Peer: &scrape.Peer{PID: 1}, UnitErr: scrape.ErrActivated}, "systemd.unit.name=queue.service,service.name=queue"},
	} {
		got := attrs(m.ResourceAttributes(&tc.src))
		want := "host.name=h,host.id=i,systemd.report.source=" + tc.src.Name + "," + tc.want
		if got != want {
			t.Errorf("%s:\n got %s\nwant %s", tc.src.Name, got, want)
		}
	}
}

func TestSelfMetrics(t *testing.T) {
	m := NewMapper(Options{HostName: "h", HostID: "i", Naming: NamingOTel})
	bad := scrape.Source{Name: "io.example.Bad", Err: fmt.Errorf("dial: refused"), Duration: time.Millisecond, UnitErr: scrape.ErrActivated}
	noisy := manager(1, 1)
	noisy.Invalid, noisy.Untyped = 2, 1
	m.Map(result(t0, bad, noisy))
	b := m.Map(result(t0.Add(time.Minute), bad, noisy))
	self := b.Resources[len(b.Resources)-1]
	if attrs(self.Attributes) != "host.name=h,host.id=i,service.name=sdmetrics" {
		t.Errorf("self attributes = %s", attrs(self.Attributes))
	}
	got := map[string]string{}
	for _, m := range self.Metrics {
		var ps []string
		for _, p := range m.Points {
			ps = append(ps, fmt.Sprintf("{%s}%v", attrs(p.Attributes), p.Value()))
		}
		got[m.Name] = strings.Join(ps, " ")
	}
	for name, want := range map[string]string{
		"sdmetrics.sources":              "{}2",
		"sdmetrics.sources.failed":       "{}1",
		"sdmetrics.source.up":            "{systemd.report.source=io.example.Bad}0 {systemd.report.source=io.systemd.Manager}1",
		"sdmetrics.source.unit.resolved": "{systemd.report.source=io.example.Bad,reason=activated}0 {systemd.report.source=io.systemd.Manager}1",
		"sdmetrics.metrics.dropped":      "{systemd.report.source=io.systemd.Manager,reason=invalid}4 {systemd.report.source=io.systemd.Manager,reason=untyped}2",
	} {
		if got[name] != want {
			t.Errorf("%s = %s, want %s", name, got[name], want)
		}
	}
	if _, ok := got["sdmetrics.scrape.duration"]; !ok {
		t.Error("no scrape duration")
	}
	if find(b, "io.systemd.Manager", "systemd.manager.n_restarts") == nil {
		t.Error("OTel naming not applied")
	}
}

func TestToSDK(t *testing.T) {
	m := NewMapper(Options{HostName: "h", HostID: "i"})
	rms := ToSDK(m.Map(result(t0, manager(3, 1))), "v1.2.3")
	if len(rms) != 2 {
		t.Fatalf("resource metrics = %d", len(rms))
	}
	rm := rms[0]
	if v, ok := rm.Resource.Set().Value("systemd.unit.name"); !ok || v.AsString() != "init.scope" {
		t.Errorf("resource = %v", rm.Resource)
	}
	sm := rm.ScopeMetrics[0]
	if sm.Scope.Name != ScopeName || sm.Scope.Version != "v1.2.3" {
		t.Errorf("scope = %+v", sm.Scope)
	}
	for _, md := range sm.Metrics {
		switch md.Name {
		case "io.systemd.Manager.NRestarts":
			s, ok := md.Data.(metricdata.Sum[int64])
			if !ok || !s.IsMonotonic || s.Temporality != metricdata.CumulativeTemporality || s.DataPoints[0].Value != 3 {
				t.Errorf("NRestarts = %#v", md.Data)
			}
		case "io.systemd.Manager.UnitsByTypeTotal":
			g, ok := md.Data.(metricdata.Gauge[float64]) // one float point makes the metric float
			if !ok || g.DataPoints[0].Value != 40 || g.DataPoints[1].Value != 2.5 {
				t.Errorf("UnitsByTypeTotal = %#v", md.Data)
			}
		case "io.systemd.Manager.UnitActiveState":
			g, ok := md.Data.(metricdata.Gauge[int64])
			if !ok || g.DataPoints[0].Value != 1 {
				t.Errorf("UnitActiveState = %#v", md.Data)
			}
			if v, _ := g.DataPoints[0].Attributes.Value(AttrValue); v.AsString() != "active" {
				t.Errorf("UnitActiveState attributes = %v", g.DataPoints[0].Attributes)
			}
		default:
			t.Errorf("unexpected metric %s", md.Name)
		}
	}
}
