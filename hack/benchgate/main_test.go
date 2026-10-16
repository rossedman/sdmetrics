package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const baseOut = `goos: linux
BenchmarkScrape200-4   	      20	  60000000 ns/op	60045820 B/op	  490987 allocs/op
BenchmarkScrape200-4   	      20	  62000000 ns/op	60045820 B/op	  490987 allocs/op
BenchmarkScrape200-4   	      20	  90000000 ns/op	60045820 B/op	  490987 allocs/op
BenchmarkGaugeSet-4    	1000000	        38 ns/op	       0 B/op	       0 allocs/op
PASS
`

func TestCompare(t *testing.T) {
	base, err := parse(strings.NewReader(baseOut))
	if err != nil {
		t.Fatal(err)
	}
	if got := median(base["BenchmarkScrape200"]); got != 62000000 {
		t.Fatalf("median = %v", got)
	}
	head, _ := parse(strings.NewReader("BenchmarkScrape200-4 20 70000000 ns/op\nBenchmarkGaugeSet-4 1 60 ns/op\nBenchmarkNew-4 1 5 ns/op\n"))
	report, failed := compare(base, head, 25)
	if !failed || !strings.Contains(report, "BenchmarkGaugeSet") || !strings.Contains(report, "REGRESSION") || !strings.Contains(report, "new") {
		t.Fatalf("failed=%v\n%s", failed, report)
	}
	if strings.Count(report, "REGRESSION") != 1 {
		t.Errorf("only GaugeSet (+58%%) regressed:\n%s", report)
	}
	if _, failed := compare(base, base, 25); failed {
		t.Error("identical runs failed")
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := write("base.txt", baseOut)
	slow := write("slow.txt", "BenchmarkGaugeSet-4 1 100 ns/op\n")
	bad := write("bad.txt", "BenchmarkX-4 1 abc ns/op\n")
	var out strings.Builder
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{base, base}, 0},
		{[]string{base, slow}, 1},
		{[]string{"-threshold", "500", base, slow}, 0},
		{[]string{base}, 2},
		{[]string{"-nope", base, base}, 2},
		{[]string{filepath.Join(dir, "missing"), base}, 2},
		{[]string{base, filepath.Join(dir, "missing")}, 2},
		{[]string{bad, base}, 2},
	} {
		if code := run(tc.args, &out, io.Discard); code != tc.code {
			t.Errorf("run(%v) = %d, want %d", tc.args, code, tc.code)
		}
	}
	if median([]float64{1, 3}) != 2 {
		t.Error("median of even count")
	}
}
