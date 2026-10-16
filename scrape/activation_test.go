package scrape

import (
	"context"
	"encoding/json"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/varlinktest"
)

func fakeManager(t *testing.T, units ...string) (*varlinktest.Server, *atomic.Int32) {
	t.Helper()
	srv := varlinktest.NewServer(t)
	calls := new(atomic.Int32)
	varlinktest.OnStream(srv, "io.systemd.Unit.List", func(context.Context, struct{}) iter.Seq2[json.RawMessage, error] {
		calls.Add(1)
		return func(yield func(json.RawMessage, error) bool) {
			for _, u := range units {
				if !yield(json.RawMessage(u), nil) {
					return
				}
			}
		}
	})
	return srv, calls
}

func TestResolveActivated(t *testing.T) {
	mgr, calls := fakeManager(t,
		`{"context":{"Type":"service","ID":"systemd-networkd.service"}}`,
		`{"context":{"Type":"socket","ID":"systemd-networkd-varlink-metrics.socket","Triggers":["systemd-networkd.service"],"Socket":{"Listen":[{"type":"Stream","address":"/run/systemd/report/io.systemd.Network"}],"Accept":false}}}`,
		`{"context":{"Type":"socket","ID":"systemd-report-basic.socket","Triggers":["systemd-report-basic@.service"],"Socket":{"Listen":[{"type":"Stream","address":"/run/systemd/report/io.systemd.Basic"}],"Accept":true}}}`,
		`{"context":{"Type":"socket","ID":"old.socket"}}`,
	)
	s := New(Options{ManagerSocket: mgr.Path()})
	srcs := []Source{
		{Name: "io.systemd.Network", Path: "/run/systemd/report/io.systemd.Network", UnitErr: ErrActivated},
		{Name: "io.systemd.Basic", Path: "/run/systemd/report/io.systemd.Basic", UnitErr: ErrActivated},
		{Name: "io.example.Unknown", Path: "/run/systemd/report/io.example.Unknown", UnitErr: ErrActivated},
		{Name: "io.example.Direct", Path: "/run/systemd/report/io.example.Direct", Unit: "direct.service"},
	}
	s.resolveActivated(context.Background(), srcs)
	for _, tc := range []struct {
		unit      string
		activated bool
		err       bool
	}{
		{"systemd-networkd.service", true, false},
		{"systemd-report-basic.socket", true, false},
		{"", true, true},
		{"direct.service", false, false},
	} {
		src := srcs[0]
		srcs = srcs[1:]
		if src.Unit != tc.unit || src.Activated != tc.activated || (src.UnitErr != nil) != tc.err {
			t.Errorf("%s: unit=%q activated=%v err=%v", src.Name, src.Unit, src.Activated, src.UnitErr)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("Unit.List called %d times in one scrape, want 1", calls.Load())
	}
	// Within the refresh interval, a missing source does not refetch.
	s.resolveActivated(context.Background(), []Source{{Name: "io.example.Unknown", Path: "/x", UnitErr: ErrActivated}})
	if calls.Load() != 1 {
		t.Errorf("Unit.List refetched within %v", socketIndexRefresh)
	}
	// After it, it does.
	s.sockets.fetched = time.Now().Add(-2 * socketIndexRefresh)
	s.resolveActivated(context.Background(), []Source{{Name: "io.example.Unknown", Path: "/x", UnitErr: ErrActivated}})
	if calls.Load() != 2 {
		t.Errorf("Unit.List not refetched after %v: %d calls", socketIndexRefresh, calls.Load())
	}
}

func TestResolveActivatedNoManager(t *testing.T) {
	s := New(Options{ManagerSocket: "/nonexistent/io.systemd.Manager", Logger: nil})
	srcs := []Source{{Name: "io.example.A", Path: "/run/systemd/report/io.example.A", UnitErr: ErrActivated}}
	s.resolveActivated(context.Background(), srcs)
	if srcs[0].Unit != "" || !srcs[0].Activated || srcs[0].UnitErr == nil {
		t.Errorf("source = %+v", srcs[0])
	}
}
