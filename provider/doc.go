// Package provider publishes a Go program's metrics through systemd's
// report framework (systemd v260+).
//
// A provider serves the io.systemd.Metrics Varlink interface on a socket
// under /run/systemd/report/, named after the provider. systemd-report,
// sdmetrics-exporter and the Collector's systemdreport receiver find the
// socket there and read the metrics.
//
//	p, err := provider.New("io.example.Queue") // socket: /run/systemd/report/io.example.Queue
//	if err != nil {
//		return err
//	}
//	depth := p.Gauge("Depth", provider.Help("Jobs waiting"))
//	jobs := p.Counter("JobsProcessed", provider.Labels("result"))
//
//	go p.Serve(ctx) // stops and removes the socket when ctx ends
//
//	depth.Set(42)
//	jobs.With("ok").Inc()
//
// # Names
//
// The provider name is a Varlink interface name in reverse-domain style,
// such as io.example.Queue, and becomes the socket's file name. Metric
// names are appended to it: the gauge above is published as the family
// io.example.Queue.Depth. systemd-report only accepts families named
// after the socket they come from, so New and the registration methods
// validate names and panic on names systemd would drop. systemd's own
// sources name families in PascalCase (io.systemd.Manager.NRestarts);
// letters, digits and single underscores are allowed.
//
// The v260 wire format carries no unit. Put the unit in the name, as in
// RequestLatencySeconds or CacheBytes; sdmetrics-exporter's OTel naming
// mode moves such suffixes into the OpenTelemetry unit field.
//
// # Values, labels and objects
//
// Counters hold unsigned integers and gauges hold float64 values, read
// and written with atomic operations: updating a metric never takes a
// lock the server holds, and does not allocate. Strings hold a string
// value, such as a state, and become info-style metrics in OpenTelemetry.
//
// Labels become the "fields" of each reply. With returns the series for
// a set of label values; keep the handle it returns on hot paths. On sets
// the reply's "object", which systemd uses for the thing a value is about,
// such as a unit or an interface name.
//
// # Socket placement
//
// Under systemd, prefer socket activation: a .socket unit with
// ListenStream=/run/systemd/report/io.example.Queue, and FromActivation
// to adopt it. Otherwise Serve binds the socket itself, mode 0660 unless
// WithSocketMode or WithSocketGroup say otherwise.
//
// # Existing instrumentation
//
// Packages prombridge and otelbridge expose a Prometheus registry or an
// OpenTelemetry SDK reader through a provider, so code that is already
// instrumented joins the report framework without changes. Any other
// source can implement Collector.
package provider
