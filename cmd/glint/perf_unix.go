//go:build darwin || linux

package main

import (
	"runtime"
	"syscall"
	"time"
)

// readRusage reads the process's own resource usage via getrusage(2).
// ru_maxrss is already a running maximum maintained by the kernel, so a
// single read at exit gives the true peak — no sampling/polling needed.
// Units differ by OS: bytes on Darwin/BSD, kilobytes on Linux.
func readRusage() rusageStats {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return rusageStats{}
	}

	maxRSS := uint64(ru.Maxrss)
	if runtime.GOOS == "linux" {
		maxRSS *= 1024
	}

	return rusageStats{
		maxRSS:   maxRSS,
		userTime: time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond,
		sysTime:  time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond,
	}
}
