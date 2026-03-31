// Package await provides polling-based test assertions as a replacement
// for testify's Eventually, EventuallyWithT, and their formatted variants.
//
// Improvements over testify's eventually functions:
//
//   - Misuse detection: accidentally using the real *testing.T (e.g. s.T() or
//     suite assertion methods) instead of the callback's collect T is a
//     common mistake. This package detects it and fails with a clear message.
//
//   - Safer bool predicates: unlike testify's Eventually, [RequireTrue] only
//     accepts func() bool, so returning false is the sole retry signal. If the
//     predicate accidentally marks the real test failed, it reports that
//     immediately instead of polling until timeout.
//
//   - Timeout-aware callbacks: callbacks receive a context derived from the
//     parent context and canceled when the await timeout or test deadline is
//     reached, so RPCs and blocking waits can exit instead of continuing after
//     the retry window has expired.
//
//   - Panic propagation: if the condition panics (e.g. nil dereference), the
//     panic is propagated immediately rather than being silently swallowed
//     or retried until timeout.
//     See https://github.com/stretchr/testify/issues/1810
//
//   - No goroutine leaks: testify's Eventually may return on timeout while
//     the condition goroutine is still running, causing "panic: Fail in
//     goroutine after Test has completed" crashes and data races. This
//     package waits for each attempt to finish before starting the next.
//     See https://github.com/stretchr/testify/issues/1611
//
//   - Condition always runs: testify's Eventually can fail without ever
//     running the condition due to a timer/ticker race with short timeouts.
//     This package runs the condition immediately on the first iteration.
//     See https://github.com/stretchr/testify/issues/1652
package await

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Require runs condition repeatedly until it completes without assertion
// failures, or until ctx is canceled or the timeout expires. The timeout is
// capped at ctx's deadline and the test's deadline if either is set.
//
// The condition receives an *await.T for assertions. Its [T.Context] returns a
// context derived from ctx and canceled when ctx is canceled, the timeout
// expires, or the test deadline is reached. Pass *await.T to require.*
// functions. When assertions fail, [Require] catches the failure and retries.
//
// A goroutine is used per attempt so accidental calls to the real test's
// FailNow terminate only the attempt, not the test.
//
// Example:
//
//	await.Require(ctx, t, func(t *await.T) {
//	    resp, err := client.GetStatus(t.Context())
//	    require.NoError(t, err)
//	    require.Equal(t, "ready", resp.Status)
//	}, 5*time.Second, 200*time.Millisecond)
func Require(ctx context.Context, tb testing.TB, condition func(*T), timeout, pollInterval time.Duration) {
	tb.Helper()
	run(ctx, tb, func(t *T) bool {
		condition(t)
		return true
	}, timeout, pollInterval, "", "await.Require",
		"use the *await.T passed to the callback, not s.T() or suite assertion methods")
}

// Requiref is like [Require] but accepts a format string that is included in the
// failure message when the condition is not satisfied before the timeout.
//
// Example:
//
//	await.Requiref(ctx, t, func(t *await.T) {
//	    require.Equal(t, "ready", status.Load())
//	}, 5*time.Second, 200*time.Millisecond, "workflow %s did not reach ready state", wfID)
func Requiref(ctx context.Context, tb testing.TB, condition func(*T), timeout, pollInterval time.Duration, msg string, args ...any) {
	tb.Helper()
	run(ctx, tb, func(t *T) bool {
		condition(t)
		return true
	}, timeout, pollInterval, fmt.Sprintf(msg, args...), "await.Require",
		"use the *await.T passed to the callback, not s.T() or suite assertion methods")
}

// RequireTrue runs condition repeatedly until it returns true, or until the
// timeout expires. The timeout is capped at the test's deadline if one is set.
//
// Use [RequireTrue] for simple local predicates only. Do not use assertions or
// side effects in the predicate; return false to retry. Use [Require] for
// assertions, context-aware work, or retryable checks that need detailed
// failure messages.
func RequireTrue(tb testing.TB, condition func() bool, timeout, pollInterval time.Duration) {
	tb.Helper()
	run(context.Background(), tb, func(*T) bool {
		return condition()
	}, timeout, pollInterval, "", "await.RequireTrue",
		"do not use test assertions inside the predicate; return false to retry or use await.Require for assertions")
}

// RequireTruef is like [RequireTrue] but accepts a format string that is included
// in the failure message when the condition is not satisfied before the timeout.
func RequireTruef(tb testing.TB, condition func() bool, timeout, pollInterval time.Duration, msg string, args ...any) {
	tb.Helper()
	run(context.Background(), tb, func(*T) bool {
		return condition()
	}, timeout, pollInterval, fmt.Sprintf(msg, args...), "await.RequireTrue",
		"do not use test assertions inside the predicate; return false to retry or use await.Require for assertions")
}

type boolAttempt struct {
	ok      bool
	stopped bool
}

func run(
	parentCtx context.Context,
	tb testing.TB,
	condition func(*T) bool,
	timeout,
	pollInterval time.Duration,
	msg string,
	name string,
	misuseHint string,
) {
	tb.Helper()

	// Skip if the test already failed — no point polling.
	if tb.Failed() {
		tb.Logf("%s: skipping (test already failed)", name)
		return
	}
	if parentCtx == nil {
		tb.Fatalf("%s: nil context", name)
		return
	}

	deadline := time.Now().Add(timeout)

	if parentDeadline, hasDeadline := parentCtx.Deadline(); hasDeadline && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}

	// Cap at the test's deadline if one is set, so we don't sleep past it.
	if d, ok := tb.(interface{ Deadline() (time.Time, bool) }); ok {
		if testDeadline, hasDeadline := d.Deadline(); hasDeadline && testDeadline.Before(deadline) {
			deadline = testDeadline
		}
	}
	ctx, cancel := context.WithDeadline(parentCtx, deadline)
	defer cancel()
	effectiveTimeout := time.Until(deadline)
	if effectiveTimeout < 0 {
		effectiveTimeout = 0
	}

	polls := 0

	for {
		if err := ctx.Err(); err != nil && !deadlineReached(deadline) {
			tb.Fatalf("%s: context canceled before condition was satisfied: %v", name, err)
			return
		}

		polls++
		t := &T{TB: tb, ctx: ctx}

		// Run condition in a goroutine so that real-test FailNow misuse
		// terminates only this goroutine, not the test.
		//
		// Channel protocol:
		//   boolAttempt → condition returned or assertion stopped the attempt
		//   panicVal    → condition panicked (propagated to caller)
		done := make(chan any, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(attemptFailed); ok {
						done <- boolAttempt{stopped: true}
						return
					}
					done <- r // propagate panic
					return
				}
				// If we reach here via Goexit (from real-test FailNow misuse),
				// send stopped. If condition completed normally, a result was
				// already sent.
				select {
				case done <- boolAttempt{stopped: true}:
				default:
				}
			}()
			done <- boolAttempt{ok: condition(t)}
		}()

		result := <-done
		switch v := result.(type) {
		case boolAttempt:
			if tb.Failed() {
				tb.Fatalf("%s: the test was marked failed directly — %s", name, misuseHint)
				return
			}
			if err := ctx.Err(); err != nil && !deadlineReached(deadline) {
				if len(t.errors) > 0 {
					tb.Errorf("last attempt errors:\n%s", strings.Join(t.errors, "\n"))
				}
				tb.Fatalf("%s: context canceled before condition was satisfied: %v", name, err)
				return
			}
			if v.ok && !v.stopped && !t.Failed() && !deadlineReached(deadline) {
				return
			}
		default:
			// Condition panicked — propagate immediately.
			panic(v)
		}

		// Detect misuse: require.NoError(s.T(), ...) inside the callback marks
		// the real test as failed via Errorf then calls FailNow (Goexit).
		if tb.Failed() {
			tb.Fatalf("%s: the test was marked failed directly — %s", name, misuseHint)
			return
		}

		if deadlineReached(deadline) {
			if len(t.errors) > 0 {
				tb.Errorf("last attempt errors:\n%s", strings.Join(t.errors, "\n"))
			}
			if msg != "" {
				tb.Fatalf("%s: %s (not satisfied after %v, %d polls)", name, msg, effectiveTimeout, polls)
			} else {
				tb.Fatalf("%s: condition not satisfied after %v (%d polls)", name, effectiveTimeout, polls)
			}
			return
		}

		sleep(ctx, deadline, pollInterval)
	}
}

func sleep(ctx context.Context, deadline time.Time, pollInterval time.Duration) {
	remaining := time.Until(deadline)
	if remaining < pollInterval {
		pollInterval = remaining
	}
	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func deadlineReached(deadline time.Time) bool {
	return !time.Now().Before(deadline)
}
