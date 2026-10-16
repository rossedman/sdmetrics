// Command scrape uses the scrape package directly: it reads every report
// source on the host once and prints each source's unit and families,
// a small systemd-report built on sdmetrics.
//
//	go run ./examples/scrape
//	go run ./examples/scrape -dir /tmp/report
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/rossedman/sdmetrics/scrape"
)

func main() {
	dir := flag.String("dir", scrape.DefaultDir, "report directory")
	flag.Parse()

	s := scrape.New(scrape.Options{Dir: *dir, Timeout: time.Second})
	res, err := s.Scrape(context.Background())
	if err != nil {
		log.Fatal(err) // scrape.ErrNoReportDir before systemd v260
	}
	for _, src := range res.Sources {
		unit := src.Unit
		if unit == "" {
			unit = fmt.Sprintf("unknown: %v", src.UnitErr)
		}
		fmt.Printf("%s (%s) in %v\n", src.Name, unit, src.Duration.Round(time.Microsecond))
		if src.Err != nil {
			fmt.Printf("  error: %v\n", src.Err)
			continue
		}
		for _, f := range src.Families {
			fmt.Printf("  %-50s %-7s %d values\n", f.Name, f.Type, len(f.Metrics))
		}
	}
}
