package exporter

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rossedman/sdmetrics/scrape"
)

// Print writes one scrape for people (format "text") or tools ("json").
func Print(w io.Writer, res *scrape.Result, format string) error {
	switch format {
	case "json":
		return printJSON(w, res)
	case "", "text":
		return printText(w, res)
	}
	return fmt.Errorf("unknown format %q: want text or json", format)
}

// printText writes res as an aligned table with a summary line.
func printText(w io.Writer, res *scrape.Result) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SOURCE\tUNIT\tFAMILY\tTYPE\tOBJECT\tFIELDS\tVALUE")
	values := 0
	for _, s := range res.Sources {
		unit := s.Unit
		if unit == "" {
			unit = "-"
		}
		if s.Err != nil {
			fmt.Fprintf(tw, "%s\t%s\t-\t-\t-\t-\terror: %v\n", s.Name, unit, s.Err)
			continue
		}
		for _, f := range s.Families {
			for _, m := range f.Metrics {
				values++
				obj := m.Object
				if obj == "" {
					obj = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, unit, f.Name, f.Type, obj, fields(m.Fields), m.Value)
			}
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%d sources, %d values in %v.\n", len(res.Sources), values, res.Duration.Round(100_000))
	return err
}

// fields formats a metric's fields as sorted key=value pairs, or "-" if empty.
func fields(m map[string]string) string {
	if len(m) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + m[k]
	}
	return strings.Join(parts, ",")
}

type jsonSource struct {
	Name       string       `json:"name"`
	Path       string       `json:"path"`
	Unit       string       `json:"unit,omitempty"`
	UnitError  string       `json:"unit_error,omitempty"`
	PID        int32        `json:"pid,omitempty"`
	Error      string       `json:"error,omitempty"`
	DurationMS float64      `json:"duration_ms"`
	Dropped    int          `json:"dropped,omitempty"`
	Families   []jsonFamily `json:"families"`
}

type jsonFamily struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Type        scrape.Type  `json:"type"`
	Metrics     []jsonMetric `json:"metrics"`
}

type jsonMetric struct {
	Object string            `json:"object,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
	Value  any               `json:"value"`
}

// printJSON writes res as indented JSON.
func printJSON(w io.Writer, res *scrape.Result) error {
	out := make([]jsonSource, 0, len(res.Sources))
	for _, s := range res.Sources {
		js := jsonSource{Name: s.Name, Path: s.Path, Unit: s.Unit, DurationMS: float64(s.Duration.Microseconds()) / 1000,
			Dropped: s.Invalid + s.Untyped + s.Truncated, Families: []jsonFamily{}}
		if s.UnitErr != nil {
			js.UnitError = s.UnitErr.Error()
		}
		if s.Peer != nil {
			js.PID = s.Peer.PID
		}
		if s.Err != nil {
			js.Error = s.Err.Error()
		}
		for _, f := range s.Families {
			jf := jsonFamily{Name: f.Name, Description: f.Description, Type: f.Type, Metrics: []jsonMetric{}}
			for _, m := range f.Metrics {
				var v any
				switch m.Value.Kind {
				case scrape.KindInt:
					v = m.Value.Int
				case scrape.KindFloat:
					v = m.Value.Float
				default:
					v = m.Value.Str
				}
				jf.Metrics = append(jf.Metrics, jsonMetric{Object: m.Object, Fields: m.Fields, Value: v})
			}
			js.Families = append(js.Families, jf)
		}
		out = append(out, js)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"time": res.Time, "sources": out})
}
