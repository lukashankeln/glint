// Package memo holds small memoization helpers shared across packages.
package memo

import "sync"

// Once memoizes a value and its error, computed at most once. The zero value
// is ready to use. A Once must not be copied after first use.
//
// An error is cached like a value: later Get calls return it without calling
// fn. Only the fn passed to the first Get ever runs. If that fn panics,
// sync.Once semantics apply: later calls return the zero value and a nil
// error.
type Once[T any] struct {
	once sync.Once
	val  T
	err  error
}

// Get returns the memoized value and error, calling fn to compute them on the
// first call only. Concurrent callers block until the first call finishes.
func (o *Once[T]) Get(fn func() (T, error)) (T, error) {
	o.once.Do(func() { o.val, o.err = fn() })
	return o.val, o.err
}
