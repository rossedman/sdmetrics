//go:generate mdatagen metadata.yaml

// Package systemdreportreceiver is an OpenTelemetry Collector receiver for
// systemd report metrics (systemd v260+). It scrapes every socket under
// /run/systemd/report/ that serves the io.systemd.Metrics Varlink interface
// and emits one resource per source, identified by its systemd unit.
package systemdreportreceiver // import "github.com/rossedman/sdmetrics/receiver/systemdreportreceiver"
