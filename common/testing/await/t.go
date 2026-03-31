package await

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

var _ testing.TB = (*T)(nil)

type attemptFailed struct{}

// T is passed to the condition callback. It intercepts assertion failures
// so the polling loop can retry.
//
// Only use T for assertions (require.*, t.Errorf, t.FailNow). Test lifecycle
// methods like t.Cleanup, t.Skip, and t.Setenv cannot be used inside polling
// conditions.
type T struct {
	testing.TB
	ctx    context.Context
	errors []string
	failed bool
}

// Context returns the await-scoped context for the current attempt.
func (t *T) Context() context.Context {
	if t.ctx != nil {
		return t.ctx
	}
	return t.TB.Context()
}

// Errorf records an error message for reporting on timeout.
func (t *T) Errorf(format string, args ...any) {
	t.Fail()
	t.errors = append(t.errors, fmt.Sprintf(format, args...))
}

// Error records an error message for reporting on timeout.
func (t *T) Error(args ...any) {
	t.Fail()
	t.errors = append(t.errors, strings.TrimSuffix(fmt.Sprintln(args...), "\n"))
}

// Fail records a failure for this attempt without marking the real test failed.
func (t *T) Fail() {
	t.failed = true
}

// FailNow is called by require.* on failure. It stops the current attempt.
// Unlike testing.TB.FailNow(), this does NOT mark the test as failed.
func (t *T) FailNow() {
	t.Fail()
	panic(attemptFailed{})
}

// Fatal records an error message and stops this attempt.
func (t *T) Fatal(args ...any) {
	t.Error(args...)
	t.FailNow()
}

// Fatalf records an error message and stops this attempt.
func (t *T) Fatalf(format string, args ...any) {
	t.Errorf(format, args...)
	t.FailNow()
}

// Failed reports whether this attempt has failed.
func (t *T) Failed() bool {
	return t.failed
}

// ArtifactDir panics because it is not supported.
func (t *T) ArtifactDir() string {
	panic("await.T.ArtifactDir cannot be used inside polling conditions")
}

// Attr panics because it is not supported.
func (t *T) Attr(string, string) {
	panic("await.T.Attr cannot be used inside polling conditions")
}

// Chdir panics because it is not supported.
func (t *T) Chdir(string) {
	panic("await.T.Chdir cannot be used inside polling conditions")
}

// Cleanup panics because it is not supported.
func (t *T) Cleanup(func()) {
	panic("await.T.Cleanup cannot be used inside polling conditions")
}

// Output panics because it is not supported.
func (t *T) Output() io.Writer {
	panic("await.T.Output cannot be used inside polling conditions")
}

// Setenv panics because it is not supported.
func (t *T) Setenv(string, string) {
	panic("await.T.Setenv cannot be used inside polling conditions")
}

// Skip panics because it is not supported.
func (t *T) Skip(...any) {
	panic("await.T.Skip cannot be used inside polling conditions")
}

// Skipf panics because it is not supported.
func (t *T) Skipf(string, ...any) {
	panic("await.T.Skipf cannot be used inside polling conditions")
}

// SkipNow panics because it is not supported.
func (t *T) SkipNow() {
	panic("await.T.SkipNow cannot be used inside polling conditions")
}

// TempDir panics because it is not supported.
func (t *T) TempDir() string {
	panic("await.T.TempDir cannot be used inside polling conditions")
}
