package memo

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOnceConcurrentComputesOnce(t *testing.T) {
	var o Once[string]
	var calls atomic.Int32
	results := make([]string, 50)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := o.Get(func() (string, error) {
				calls.Add(1)
				return "value", nil
			})
			assert.NoError(t, err)
			results[i] = v
		}()
	}
	wg.Wait()

	assert.EqualValues(t, 1, calls.Load())
	for _, r := range results {
		assert.Equal(t, "value", r)
	}
}

func TestOnceCachesError(t *testing.T) {
	var o Once[int]
	boom := errors.New("boom")
	calls := 0
	_, err := o.Get(func() (int, error) { calls++; return 0, boom })
	require.ErrorIs(t, err, boom)

	_, err = o.Get(func() (int, error) { calls++; return 1, nil })
	require.ErrorIs(t, err, boom)
	assert.Equal(t, 1, calls)
}

func TestOnceZeroValueUsable(t *testing.T) {
	var o Once[int]
	v, err := o.Get(func() (int, error) { return 42, nil })
	require.NoError(t, err)
	assert.Equal(t, 42, v)
}
