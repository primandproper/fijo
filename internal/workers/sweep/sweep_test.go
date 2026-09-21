package sweep

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestRunner(t *testing.T) {
	t.Parallel()

	t.Run("sweeps on the interval and stops on Close", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		r, err := New("test", 5*time.Millisecond, func(context.Context) (int64, error) {
			calls.Add(1)

			return 1, nil
		}, nil)
		must.NoError(t, err)

		go r.Run()

		deadline := time.Now().Add(2 * time.Second)
		for calls.Load() < 2 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}

		must.True(t, calls.Load() >= 2, must.Sprint("the sweeper never ran"))

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		must.NoError(t, r.Close(ctx))
		must.NoError(t, r.Close(ctx), must.Sprint("Close is idempotent"))
	})

	t.Run("a failing sweep keeps the loop alive", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		r, err := New("failing", 5*time.Millisecond, func(context.Context) (int64, error) {
			calls.Add(1)

			return 0, errors.New("boom")
		}, nil)
		must.NoError(t, err)

		go r.Run()

		deadline := time.Now().Add(2 * time.Second)
		for calls.Load() < 3 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}

		test.True(t, calls.Load() >= 3)

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		must.NoError(t, r.Close(ctx))
	})

	t.Run("refuses what it cannot run", func(t *testing.T) {
		t.Parallel()

		_, err := New("", time.Second, func(context.Context) (int64, error) { return 0, nil }, nil)
		test.ErrorIs(t, err, ErrEmptyName)

		_, err = New("x", 0, func(context.Context) (int64, error) { return 0, nil }, nil)
		test.ErrorIs(t, err, ErrNonPositiveGap)

		_, err = New("x", time.Second, nil, nil)
		test.ErrorIs(t, err, ErrNilSweep)
	})
}
