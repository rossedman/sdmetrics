package scrape_test

import (
	"context"
	"fmt"
	"log"

	"github.com/rossedman/sdmetrics/scrape"
)

func ExampleScraper_Scrape() {
	s := scrape.New(scrape.Options{Include: []string{"io.systemd.*"}})
	res, err := s.Scrape(context.Background())
	if err != nil {
		log.Fatal(err) // scrape.ErrNoReportDir before systemd v260
	}
	for _, src := range res.Sources {
		if src.Err != nil {
			log.Printf("%s: %v", src.Name, src.Err) // one source never fails the batch
			continue
		}
		for _, f := range src.Families {
			for _, m := range f.Metrics {
				fmt.Println(src.Unit, f.Name, m.Object, m.Fields, m.Value)
			}
		}
	}
}
