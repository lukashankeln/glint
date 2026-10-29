// Package fsutil holds small filesystem helpers shared across packages.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrNotRegular is wrapped by ReadFileCapped when the path (after following
// symlinks) is not a regular file, e.g. a device or a FIFO.
var ErrNotRegular = errors.New("not a regular file")

// ErrTooLarge is wrapped by ReadFileCapped when the file exceeds the limit.
var ErrTooLarge = errors.New("file too large")

// CheckFileCapped reports whether path, after following symlinks, is a regular
// file of at most limit bytes. It only Stats the path and never opens it, so a
// FIFO or device cannot block or exhaust memory here. The Stat error is
// returned unchanged; otherwise the errors wrap ErrNotRegular or ErrTooLarge.
//
// Accepted race: the file can be swapped between this check and a later read
// by another reader. Repository content is static input, so this is not
// defended against.
func CheckFileCapped(path string, limit int) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", path, ErrNotRegular)
	}
	if info.Size() > int64(limit) {
		return fmt.Errorf("file is %d bytes, over the %d-byte limit: %w", info.Size(), limit, ErrTooLarge)
	}
	return nil
}

// ReadFileCapped reads path, which must be a regular file of at most limit
// bytes. Symlinks are followed. A non-regular target is rejected from its
// Stat result without being opened, so a FIFO or device (e.g. a symlink to
// /dev/zero) can neither block nor exhaust memory. The size is checked before
// reading and the read itself is bounded by limit+1 bytes.
//
// Accepted race: the file can be swapped for a FIFO between the Stat and the
// Open, in which case Open can still block. Repository content is static
// input, so this is not defended against.
func ReadFileCapped(path string, limit int) ([]byte, error) {
	if err := CheckFileCapped(path, limit); err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", path, ErrNotRegular)
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("file is over the %d-byte limit: %w", limit, ErrTooLarge)
	}
	return data, nil
}
