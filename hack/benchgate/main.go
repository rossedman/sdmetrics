// Command benchgate compares two `go test -bench` outputs and fails when a
// benchmark's median ns/op regressed by more than a threshold. CI runs it
// on the pull request against its base branch:
//
//	go run ./hack/benchgate -threshold 25 base.txt head.txt
//
// Medians over several runs (-count) keep one noisy run from failing the
// check; benchstat prints the full comparison for people.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("benchgate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	threshold := fs.Float64("threshold", 25, "allowed slowdown in percent")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: benchgate [-threshold pct] base.txt head.txt")
		return 2
	}
	base, err := parseFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	head, err := parseFile(fs.Arg(1))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	report, failed := compare(base, head, *threshold)
	fmt.Fprint(stdout, report)
	if failed {
		return 1
	}
	return 0
}

func parseFile(name string) (map[string][]float64, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parse(f)
}

// parse collects ns/op samples per benchmark name, without the -N suffix.
func parse(r io.Reader) (map[string][]float64, error) {
	out := map[string][]float64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || !strings.HasPrefix(f[0], "Benchmark") {
			continue
		}
		name := f[0]
		if i := strings.LastIndexByte(name, '-'); i > 0 {
			if _, err := strconv.Atoi(name[i+1:]); err == nil {
				name = name[:i]
			}
		}
		for i := 2; i+1 < len(f); i += 2 {
			if f[i+1] == "ns/op" {
				v, err := strconv.ParseFloat(f[i], 64)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", f[0], err)
				}
				out[name] = append(out[name], v)
			}
		}
	}
	return out, sc.Err()
}

func median(xs []float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func compare(base, head map[string][]float64, threshold float64) (string, bool) {
	names := make([]string, 0, len(head))
	for n := range head {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	failed := false
	fmt.Fprintf(&b, "%-40s %14s %14s %8s\n", "benchmark", "base ns/op", "head ns/op", "delta")
	for _, n := range names {
		h := median(head[n])
		bs, ok := base[n]
		if !ok {
			fmt.Fprintf(&b, "%-40s %14s %14.0f %8s\n", n, "-", h, "new")
			continue
		}
		m := median(bs)
		delta := (h - m) / m * 100
		mark := ""
		if delta > threshold {
			mark = "  REGRESSION"
			failed = true
		}
		fmt.Fprintf(&b, "%-40s %14.0f %14.0f %+7.1f%%%s\n", n, m, h, delta, mark)
	}
	return b.String(), failed
}
