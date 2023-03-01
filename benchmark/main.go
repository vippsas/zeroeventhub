package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	zeroeventhub "github.com/vippsas/zeroeventhub/go"
	"os"
	"time"
)

type EventStatsReceiver struct {
	EventCount int
	Cursor     string
}

func (s *EventStatsReceiver) Event(partitionID int, headers map[string]string, Data json.RawMessage) error {
	s.EventCount++
	return nil
}

func (s *EventStatsReceiver) Checkpoint(partitionID int, cursor string) error {
	s.Cursor = cursor
	return nil
}

func worker(url string, tail bool, statsChan chan int) {
	c := zeroeventhub.NewClient(url, 1)
	var cursor string
	if tail {
		cursor = "_last"
	} else {
		cursor = "_first"
	}
	for {
		page := EventStatsReceiver{}
		cursors := []zeroeventhub.Cursor{
			{
				PartitionID: 0,
				Cursor:      cursor,
			},
		}
		if err := c.FetchEvents(context.TODO(), cursors, 1000, &page); err != nil {
			fmt.Fprintln(os.Stderr, "Got error: "+err.Error())
			continue
		}

		if tail {
			statsChan <- 1
			time.Sleep(50 * time.Millisecond)
		} else {
			statsChan <- page.EventCount
		}
		cursor = page.Cursor
		if cursor == "" {
			return
		}
	}

}

func main() {
	threadcount := flag.Int("n", 1, "Number of threads")
	url := flag.String("u", "", "ZeroEventHub endpoint to call")
	tail := flag.Bool("t", false, "Tail-mode -- benchmark polling inserts instead of reconstitution")
	flag.Parse()

	if *tail {
		fmt.Println("inserts-mode, stats are polls/seq")
	} else {
		fmt.Println("reconstitution mode, stats are events/seq")
	}

	statsChan := make(chan int)
	for i := 0; i != *threadcount; i++ {
		go worker(*url, *tail, statsChan)
	}

	type row struct {
		t     time.Time
		total int
	}

	total := 0
	tstart := time.Now()
	lastPrint := tstart
	var rows []row
	for {
		var s int
		s = <-statsChan
		total += s

		now := time.Now()

		rows = append(rows, row{now, total})

		dtPrint := now.Sub(lastPrint)
		if dtPrint > time.Second {
			tenAgo := len(rows) - 10
			if tenAgo < 0 {
				tenAgo = 0
			}

			windowEnd := rows[len(rows)-1]
			windowStart := rows[tenAgo]
			rate := float64(windowEnd.total-windowStart.total) / (windowEnd.t.Sub(windowStart.t).Seconds())

			fmt.Printf("stats total=%d rate/sec=%.2f\n",
				total,
				rate,
			)
			lastPrint = now
		}
	}

}
