//go:build !darwin && !linux

package main

// readRusage has no portable implementation outside darwin/linux; peak RSS
// and CPU time are reported as unavailable on these platforms.
func readRusage() rusageStats {
	return rusageStats{}
}
