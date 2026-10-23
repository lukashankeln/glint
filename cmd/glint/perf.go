package main

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

// rusageStats holds OS-reported resource usage for this process, as opposed
// to Go's internal runtime.MemStats which only sees the Go heap and can't
// report a true peak (it's a live snapshot, not a running maximum).
type rusageStats struct {
	maxRSS   uint64 // peak resident set size, in bytes; 0 if unsupported on this platform
	userTime time.Duration
	sysTime  time.Duration
}

// printPerfStats reports wall-clock runtime, peak RSS, and CPU time, enabled
// via GLINT_PERF=1. User+sys CPU time is reported alongside wall-clock because
// glint renders apps on a worker pool: with real parallelism, CPU time can run
// well above wall-clock, which total-allocated-bytes or elapsed time alone
// wouldn't reveal.
func printPerfStats(elapsed time.Duration) {
	ru := readRusage()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	rss := "n/a"
	if ru.maxRSS > 0 {
		rss = humanBytes(ru.maxRSS)
	}

	fmt.Fprintf(os.Stderr, "\nperf: runtime=%s  cpu=%s(user)+%s(sys)  peak_rss=%s  allocated=%s  gc_runs=%d\n",
		elapsed.Round(time.Millisecond),
		ru.userTime.Round(time.Millisecond), ru.sysTime.Round(time.Millisecond),
		rss, humanBytes(m.TotalAlloc), m.NumGC)
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
