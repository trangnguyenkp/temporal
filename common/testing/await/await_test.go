package await

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequire_ImmediateSuccess(t *testing.T) {
	called := 0
	Require(t.Context(), t, func(t *T) {
		called++
		require.True(t, true) //nolint:testifylint
	}, time.Second, 10*time.Millisecond)

	require.Equal(t, 1, called, "condition should be called exactly once")
}

func TestRequire_PropagatesParentContextValues(t *testing.T) {
	type contextKey struct{}

	ctx := context.WithValue(t.Context(), contextKey{}, "value")
	Require(ctx, t, func(t *T) {
		require.Equal(t, "value", t.Context().Value(contextKey{}))
	}, time.Second, 10*time.Millisecond)
}

func TestRequire_SetsTimeoutContextDeadline(t *testing.T) {
	Require(t.Context(), t, func(t *T) {
		deadline, ok := t.Context().Deadline()
		require.True(t, ok)
		require.WithinDuration(t, time.Now().Add(500*time.Millisecond), deadline, 50*time.Millisecond)
	}, 500*time.Millisecond, time.Millisecond)
}

func TestRequire_EventualSuccess(t *testing.T) {
	var counter atomic.Int32
	go func() {
		time.Sleep(50 * time.Millisecond) //nolint:forbidigo
		counter.Store(42)
	}()

	Require(t.Context(), t, func(t *T) {
		require.Equal(t, int32(42), counter.Load())
	}, time.Second, 10*time.Millisecond)
}

func TestRequire_MultipleAssertions(t *testing.T) {
	type state struct {
		count  atomic.Int32
		ready  atomic.Bool
		status atomic.Value
	}

	s := &state{}
	s.status.Store("initializing")

	go func() {
		time.Sleep(20 * time.Millisecond) //nolint:forbidigo
		s.count.Store(5)
		time.Sleep(20 * time.Millisecond) //nolint:forbidigo
		s.status.Store("ready")
		s.ready.Store(true)
	}()

	Require(t.Context(), t, func(t *T) {
		require.True(t, s.ready.Load(), "should be ready")
		require.Equal(t, int32(5), s.count.Load(), "count should be 5")
		require.Equal(t, "ready", s.status.Load().(string), "status should be ready")
	}, time.Second, 10*time.Millisecond)
}

func TestRequire_RetriesUntilSuccess(t *testing.T) {
	var attempts atomic.Int32
	var ready atomic.Bool

	go func() {
		time.Sleep(100 * time.Millisecond) //nolint:forbidigo
		ready.Store(true)
	}()

	Require(t.Context(), t, func(t *T) {
		attempts.Add(1)
		require.True(t, ready.Load())
	}, time.Second, 10*time.Millisecond)

	require.Greater(t, attempts.Load(), int32(1), "should have retried multiple times")
}

func TestRequire_FailNowStopsIteration(t *testing.T) {
	// When require.* fails, it calls FailNow which should stop the current
	// iteration but allow retry
	var attempts atomic.Int32
	var ready atomic.Bool

	go func() {
		time.Sleep(50 * time.Millisecond) //nolint:forbidigo
		ready.Store(true)
	}()

	Require(t.Context(), t, func(t *T) {
		attempts.Add(1)
		if !ready.Load() {
			require.Fail(t, "not ready yet")
			// This line should not execute after FailNow in require.Fail
			t.Error("should not reach here")
		}
	}, time.Second, 10*time.Millisecond)

	require.True(t, ready.Load())
	require.Greater(t, attempts.Load(), int32(1))
}

func TestRequire_CodeAfterFailureNotExecuted(t *testing.T) {
	var reachedAfterFailure atomic.Int32
	var ready atomic.Bool

	go func() {
		time.Sleep(50 * time.Millisecond) //nolint:forbidigo
		ready.Store(true)
	}()

	Require(t.Context(), t, func(t *T) {
		require.True(t, ready.Load(), "not ready")
		// This should only execute on the successful iteration
		reachedAfterFailure.Add(1)
	}, time.Second, 10*time.Millisecond)

	require.Equal(t, int32(1), reachedAfterFailure.Load(),
		"code after failing require should only run once (on success)")
}

func TestRequire_ErrorRetries(t *testing.T) {
	var attempts atomic.Int32

	Require(t.Context(), t, func(t *T) {
		if attempts.Add(1) < 3 {
			t.Error("not ready")
		}
	}, time.Second, 10*time.Millisecond)

	require.Equal(t, int32(3), attempts.Load())
}

func TestRequire_FailRetries(t *testing.T) {
	var attempts atomic.Int32

	Require(t.Context(), t, func(t *T) {
		if attempts.Add(1) < 3 {
			t.Fail()
			return
		}
	}, time.Second, 10*time.Millisecond)

	require.Equal(t, int32(3), attempts.Load())
}

func TestRequire_FatalRetries(t *testing.T) {
	var attempts atomic.Int32

	Require(t.Context(), t, func(t *T) {
		if attempts.Add(1) < 3 {
			t.Fatal("not ready")
		}
	}, time.Second, 10*time.Millisecond)

	require.Equal(t, int32(3), attempts.Load())
}

func TestRequire_FatalfRetries(t *testing.T) {
	var attempts atomic.Int32

	Require(t.Context(), t, func(t *T) {
		if attempts.Add(1) < 3 {
			t.Fatalf("not ready: %d", attempts.Load())
		}
	}, time.Second, 10*time.Millisecond)

	require.Equal(t, int32(3), attempts.Load())
}

func TestRequire_TickTimingRespected(t *testing.T) {
	// Verify that poll interval is actually respected — a slow condition
	// should not compress the interval between attempts.
	var attempts atomic.Int32
	start := time.Now()

	Require(t.Context(), t, func(t *T) {
		n := attempts.Add(1)
		if n < 4 {
			require.Fail(t, "not yet")
		}
	}, time.Second, 25*time.Millisecond)

	elapsed := time.Since(start)
	// 3 failures × 25ms poll = at least 75ms before 4th attempt succeeds
	require.GreaterOrEqual(t, elapsed, 60*time.Millisecond, "should respect poll interval")
	require.Equal(t, int32(4), attempts.Load())
}

func TestRequire_FailureScenarios(t *testing.T) {
	t.Run("timeout fails test", func(t *testing.T) {
		requireRecordingFatal(t, "not satisfied after", func(tb *recordingTB) {
			Require(context.Background(), tb, func(t *T) {
				require.True(t, false, "never succeeds") //nolint:testifylint
			}, 50*time.Millisecond, 10*time.Millisecond)
		})
	})

	t.Run("cancels context on timeout", func(t *testing.T) {
		requireRecordingFatal(t, "not satisfied after", func(tb *recordingTB) {
			Require(context.Background(), tb, func(t *T) {
				<-t.Context().Done()
				require.ErrorIs(t, t.Context().Err(), context.DeadlineExceeded)
			}, 50*time.Millisecond, time.Second)
		})
	})

	t.Run("parent context deadline caps callback context", func(t *testing.T) {
		parentCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		requireRecordingFatal(t, "not satisfied after", func(tb *recordingTB) {
			Require(parentCtx, tb, func(t *T) {
				deadline, ok := t.Context().Deadline()
				require.True(t, ok)
				require.Less(t, time.Until(deadline), 500*time.Millisecond)
				<-t.Context().Done()
				require.ErrorIs(t, t.Context().Err(), context.DeadlineExceeded)
			}, time.Second, time.Second)
		})
	})

	t.Run("parent context cancellation stops polling", func(t *testing.T) {
		parentCtx, cancel := context.WithCancel(context.Background())
		var attempts atomic.Int32

		requireRecordingFatal(t, "context canceled before condition was satisfied", func(tb *recordingTB) {
			Require(parentCtx, tb, func(t *T) {
				attempts.Add(1)
				t.Error("not ready")
				cancel()
			}, time.Second, time.Millisecond)
		})
		cancel()

		require.Equal(t, int32(1), attempts.Load(), "expected cancellation to stop polling")
	})

	t.Run("timeout reports last attempt errors", func(t *testing.T) {
		requireRecordingError(t, "last attempt errors", func(tb *recordingTB) {
			Require(context.Background(), tb, func(t *T) {
				require.Equal(t, "expected", "actual", "values must match")
				require.Equal(t, 1, 2, "numbers must match")
			}, 50*time.Millisecond, 10*time.Millisecond)
		})
	})

	t.Run("Requiref includes message on timeout", func(t *testing.T) {
		requireRecordingFatal(t, "workflow wf-123 not ready", func(tb *recordingTB) {
			Requiref(context.Background(), tb, func(t *T) {
				require.True(t, false) //nolint:testifylint
			}, 50*time.Millisecond, 10*time.Millisecond, "workflow %s not ready", "wf-123")
		})
	})

	t.Run("panic propagates", func(t *testing.T) {
		require.PanicsWithValue(t, "unexpected nil pointer", func() {
			Require(t.Context(), t, func(_ *T) {
				panic("unexpected nil pointer")
			}, 100*time.Millisecond, 10*time.Millisecond)
		})
	})

	t.Run("detects misuse of real T", func(t *testing.T) {
		requireRecordingFatal(t, "use the *await.T", func(tb *recordingTB) {
			Require(context.Background(), tb, func(_ *T) {
				tb.Errorf("wrong t used")
				tb.FailNow()
			}, 100*time.Millisecond, 10*time.Millisecond)
		})
	})

	t.Run("detects misuse on success path", func(t *testing.T) {
		requireRecordingFatal(t, "use the *await.T", func(tb *recordingTB) {
			Require(context.Background(), tb, func(_ *T) {
				tb.Errorf("assert-style misuse")
			}, 100*time.Millisecond, 10*time.Millisecond)
		})
	})

	t.Run("skips when already failed", func(t *testing.T) {
		conditionCalled := false
		tb := runWithRecordingTB(func(tb *recordingTB) {
			tb.Errorf("previous failure")
			Require(context.Background(), tb, func(_ *T) {
				conditionCalled = true
			}, time.Second, 10*time.Millisecond)
		})
		require.True(t, tb.Failed())
		require.False(t, conditionCalled, "condition should not run when test already failed")
	})
}

func TestRequire_NoGoroutineLeak(t *testing.T) {
	// Run Require, then verify no leftover goroutines from the polling loop.
	before := runtime.NumGoroutine()

	var ready atomic.Bool
	go func() {
		time.Sleep(30 * time.Millisecond) //nolint:forbidigo
		ready.Store(true)
	}()

	Require(t.Context(), t, func(t *T) {
		require.True(t, ready.Load())
	}, time.Second, 10*time.Millisecond)

	// Give goroutines a moment to clean up
	time.Sleep(50 * time.Millisecond) //nolint:forbidigo
	after := runtime.NumGoroutine()

	// Allow a small delta for background runtime goroutines
	require.InDelta(t, before, after, 2, "goroutine count should not grow: before=%d after=%d", before, after)
}

func TestRequireTrue_ImmediateSuccess(t *testing.T) {
	called := 0
	RequireTrue(t, func() bool {
		called++
		return true
	}, time.Second, 10*time.Millisecond)

	require.Equal(t, 1, called, "condition should be called exactly once")
}

func TestRequireTrue_EventualSuccess(t *testing.T) {
	var attempts atomic.Int32
	var ready atomic.Bool

	go func() {
		time.Sleep(50 * time.Millisecond) //nolint:forbidigo
		ready.Store(true)
	}()

	RequireTrue(t, func() bool {
		attempts.Add(1)
		return ready.Load()
	}, time.Second, 10*time.Millisecond)

	require.True(t, ready.Load())
	require.Greater(t, attempts.Load(), int32(1))
}

func TestRequireTrue_FailureScenarios(t *testing.T) {
	t.Run("timeout fails test", func(t *testing.T) {
		requireRecordingFatal(t, "not satisfied after", func(tb *recordingTB) {
			RequireTrue(tb, func() bool {
				return false
			}, 50*time.Millisecond, 10*time.Millisecond)
		})
	})

	t.Run("RequireTruef includes message on timeout", func(t *testing.T) {
		requireRecordingFatal(t, "workflow wf-123 not ready", func(tb *recordingTB) {
			RequireTruef(tb, func() bool {
				return false
			}, 50*time.Millisecond, 10*time.Millisecond, "workflow %s not ready", "wf-123")
		})
	})

	t.Run("panic propagates", func(t *testing.T) {
		require.PanicsWithValue(t, "unexpected nil pointer", func() {
			RequireTrue(t, func() bool {
				panic("unexpected nil pointer")
			}, 100*time.Millisecond, 10*time.Millisecond)
		})
	})

	t.Run("detects assertion misuse of real T", func(t *testing.T) {
		requireRecordingFatal(t, "do not use test assertions", func(tb *recordingTB) {
			RequireTrue(tb, func() bool {
				tb.Errorf("wrong t used")
				tb.FailNow()
				return false
			}, 100*time.Millisecond, 10*time.Millisecond)
		})
	})

	t.Run("detects assertion misuse on false path", func(t *testing.T) {
		requireRecordingFatal(t, "do not use test assertions", func(tb *recordingTB) {
			RequireTrue(tb, func() bool {
				tb.Errorf("assert-style misuse")
				return false
			}, 100*time.Millisecond, 10*time.Millisecond)
		})
	})

	t.Run("detects assertion misuse on success path", func(t *testing.T) {
		requireRecordingFatal(t, "do not use test assertions", func(tb *recordingTB) {
			RequireTrue(tb, func() bool {
				tb.Errorf("assert-style misuse")
				return true
			}, 100*time.Millisecond, 10*time.Millisecond)
		})
	})

	t.Run("skips when already failed", func(t *testing.T) {
		conditionCalled := false
		tb := runWithRecordingTB(func(tb *recordingTB) {
			tb.Errorf("previous failure")
			RequireTrue(tb, func() bool {
				conditionCalled = true
				return true
			}, time.Second, 10*time.Millisecond)
		})
		require.True(t, tb.Failed())
		require.False(t, conditionCalled, "condition should not run when test already failed")
	})
}

// recordingTB is a minimal testing.TB implementation for testing failure scenarios.
type recordingTB struct {
	testing.TB // embed for interface satisfaction
	mu         sync.Mutex
	failed     bool
	errors     []string
	fatals     []string
}

func (r *recordingTB) Helper()             {}
func (r *recordingTB) Name() string        { return "recording" }
func (r *recordingTB) Failed() bool        { r.mu.Lock(); defer r.mu.Unlock(); return r.failed }
func (r *recordingTB) Cleanup(fn func())   {}
func (r *recordingTB) Logf(string, ...any) {}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = true
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.mu.Lock()
	r.failed = true
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
	r.mu.Unlock()
	runtime.Goexit()
}

func (r *recordingTB) FailNow() {
	r.mu.Lock()
	r.failed = true
	r.mu.Unlock()
	runtime.Goexit()
}

func (r *recordingTB) hasFatal(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return hasMessage(r.fatals, substr)
}

func (r *recordingTB) hasError(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return hasMessage(r.errors, substr)
}

func hasMessage(messages []string, substr string) bool {
	for _, msg := range messages {
		if strings.Contains(msg, substr) {
			return true
		}
	}
	return false
}

func runWithRecordingTB(fn func(tb *recordingTB)) *recordingTB {
	tb := &recordingTB{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(tb)
	}()
	<-done
	return tb
}

func requireRecordingFatal(t *testing.T, substr string, fn func(tb *recordingTB)) *recordingTB {
	t.Helper()
	tb := runWithRecordingTB(fn)
	tb.requireFailedWithFatal(t, substr)
	return tb
}

func requireRecordingError(t *testing.T, substr string, fn func(tb *recordingTB)) *recordingTB {
	t.Helper()
	tb := runWithRecordingTB(fn)
	tb.requireFailedWithError(t, substr)
	return tb
}

func (r *recordingTB) requireFailedWithFatal(t *testing.T, substr string) {
	t.Helper()
	require.True(t, r.Failed(), "expected the recording TB to be marked as failed")
	require.Truef(t, r.hasFatal(substr), "expected fatal containing %q", substr)
}

func (r *recordingTB) requireFailedWithError(t *testing.T, substr string) {
	t.Helper()
	require.True(t, r.Failed(), "expected the recording TB to be marked as failed")
	require.Truef(t, r.hasError(substr), "expected error containing %q", substr)
}
