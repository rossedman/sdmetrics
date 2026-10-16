package systemdreportreceiver // import "github.com/rossedman/sdmetrics/receiver/systemdreportreceiver"

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"

	"github.com/rossedman/sdmetrics/otelmap"
	"github.com/rossedman/sdmetrics/receiver/systemdreportreceiver/internal/metadata"
	"github.com/rossedman/sdmetrics/scrape"
)

type reportScraper struct {
	cfg     *Config
	log     *zap.Logger
	version string
	scraper *scrape.Scraper
	mapper  *otelmap.Mapper

	noDirOnce sync.Once
}

func newReportScraper(cfg *Config, set receiver.Settings) *reportScraper {
	naming, _ := cfg.naming()
	return &reportScraper{
		cfg:     cfg,
		log:     set.Logger,
		version: set.BuildInfo.Version,
		scraper: scrape.New(scrape.Options{
			Dir:         cfg.Directory,
			Include:     cfg.Include,
			Exclude:     cfg.Exclude,
			Concurrency: cfg.Concurrency,
			Timeout:     cfg.SourceTimeout,
			Logger:      slog.New(zapslog.NewHandler(set.Logger.Core())),
		}),
		mapper: otelmap.NewMapper(otelmap.Options{
			Naming:       naming,
			Resource:     cfg.Resource,
			ServiceNames: cfg.ServiceNames,
			Units:        cfg.Units,
			ScopeVersion: set.BuildInfo.Version,
		}),
	}
}

func (s *reportScraper) scrape(ctx context.Context) (pmetric.Metrics, error) {
	res, err := s.scraper.Scrape(ctx)
	if errors.Is(err, scrape.ErrNoReportDir) {
		// Hosts older than systemd v260: report no sources, once.
		s.noDirOnce.Do(func() {
			s.log.Warn("no report directory; systemd v260 or newer is required", zap.String("directory", s.cfg.Directory))
		})
		return pmetric.NewMetrics(), nil
	}
	if err != nil {
		return pmetric.NewMetrics(), err
	}
	for _, src := range res.Sources {
		if src.Err != nil {
			s.log.Debug("source failed", zap.String("source", src.Name), zap.Error(src.Err))
		}
	}
	b := s.mapper.Map(res)
	if !s.cfg.SelfMetrics {
		b.Resources = b.Resources[:len(b.Resources)-1] // the self-metrics resource is last
	}
	return toPdata(b, s.version), nil
}

// toPdata converts a mapped batch to Collector pdata.
func toPdata(b otelmap.Batch, version string) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rms := md.ResourceMetrics()
	rms.EnsureCapacity(len(b.Resources))
	for _, r := range b.Resources {
		rm := rms.AppendEmpty()
		putAttrs(rm.Resource().Attributes(), r.Attributes)
		sm := rm.ScopeMetrics().AppendEmpty()
		sm.Scope().SetName(metadata.ScopeName)
		sm.Scope().SetVersion(version)
		ms := sm.Metrics()
		ms.EnsureCapacity(len(r.Metrics))
		for _, m := range r.Metrics {
			pm := ms.AppendEmpty()
			pm.SetName(m.Name)
			pm.SetDescription(m.Description)
			pm.SetUnit(m.Unit)
			var dps pmetric.NumberDataPointSlice
			if m.Kind == otelmap.Sum {
				sum := pm.SetEmptySum()
				sum.SetIsMonotonic(m.Monotonic)
				sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
				dps = sum.DataPoints()
			} else {
				dps = pm.SetEmptyGauge().DataPoints()
			}
			dps.EnsureCapacity(len(m.Points))
			for _, p := range m.Points {
				dp := dps.AppendEmpty()
				putAttrs(dp.Attributes(), p.Attributes)
				dp.SetTimestamp(pcommon.NewTimestampFromTime(p.Time))
				if !p.Start.IsZero() {
					dp.SetStartTimestamp(pcommon.NewTimestampFromTime(p.Start))
				}
				if p.IsInt {
					dp.SetIntValue(p.Int)
				} else {
					dp.SetDoubleValue(p.Float)
				}
			}
		}
	}
	return md
}

func putAttrs(m pcommon.Map, attrs []otelmap.Attribute) {
	m.EnsureCapacity(len(attrs))
	for _, a := range attrs {
		if a.IsInt {
			m.PutInt(a.Key, a.Int)
		} else {
			m.PutStr(a.Key, a.Str)
		}
	}
}
