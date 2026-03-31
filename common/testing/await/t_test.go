package await

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestT_TestingTBMethods(t *testing.T) {
	required := make(map[string]struct{})
	for method := range reflect.TypeFor[testing.TB]().Methods() {
		if method.IsExported() {
			required[method.Name] = struct{}{}
		}
	}

	type contextKey struct{}

	var prevName string
	for _, tc := range []struct {
		name       string
		fn         func(*T)
		assert     func(*testing.T, *T)
		panicValue any
	}{
		{
			name: "ArtifactDir",
			fn: func(t *T) {
				_ = t.ArtifactDir()
			},
			panicValue: "await.T.ArtifactDir cannot be used inside polling conditions",
		},
		{
			name: "Attr",
			fn: func(t *T) {
				t.Attr("key", "value")
			},
			panicValue: "await.T.Attr cannot be used inside polling conditions",
		},
		{
			name: "Chdir",
			fn: func(t *T) {
				t.Chdir("/tmp")
			},
			panicValue: "await.T.Chdir cannot be used inside polling conditions",
		},
		{
			name: "Cleanup",
			fn: func(t *T) {
				t.Cleanup(func() {})
			},
			panicValue: "await.T.Cleanup cannot be used inside polling conditions",
		},
		{
			name: "Context",
			fn: func(t *T) {
				t.ctx = context.WithValue(context.Background(), contextKey{}, "value")
				_ = t.Context()
			},
			assert: func(t *testing.T, at *T) {
				require.Equal(t, "value", at.Context().Value(contextKey{}))
			},
		},
		{
			name: "Error",
			fn: func(t *T) {
				t.Error("error:", "details")
			},
			assert: func(t *testing.T, at *T) {
				require.Equal(t, []string{"error: details"}, at.errors)
				require.True(t, at.Failed())
			},
		},
		{
			name: "Errorf",
			fn: func(t *T) {
				t.Errorf("first: %d", 1)
				t.Errorf("second: %d", 2)
			},
			assert: func(t *testing.T, at *T) {
				require.Equal(t, []string{"first: 1", "second: 2"}, at.errors)
				require.True(t, at.Failed())
			},
		},
		{
			name: "Fail",
			fn: func(t *T) {
				t.Fail()
			},
			assert: func(t *testing.T, at *T) {
				require.True(t, at.Failed())
			},
		},
		{
			name: "Failed",
			fn: func(t *T) {
				t.Fail()
				_ = t.Failed()
			},
			assert: func(t *testing.T, at *T) {
				require.True(t, at.Failed())
			},
		},
		{
			name: "FailNow",
			fn: func(t *T) {
				t.FailNow()
			},
			panicValue: attemptFailed{},
			assert: func(t *testing.T, at *T) {
				require.True(t, at.Failed())
			},
		},
		{
			name: "Fatal",
			fn: func(t *T) {
				t.Fatal("not ready")
			},
			panicValue: attemptFailed{},
			assert: func(t *testing.T, at *T) {
				require.Equal(t, []string{"not ready"}, at.errors)
				require.True(t, at.Failed())
			},
		},
		{
			name: "Fatalf",
			fn: func(t *T) {
				t.Fatalf("not ready: %d", 1)
			},
			panicValue: attemptFailed{},
			assert: func(t *testing.T, at *T) {
				require.Equal(t, []string{"not ready: 1"}, at.errors)
				require.True(t, at.Failed())
			},
		},
		{
			name: "Helper",
			fn: func(t *T) {
				t.Helper()
			},
			assert: func(*testing.T, *T) {
				// Delegates to the embedded testing.TB.
			},
		},
		{
			name: "Log",
			fn: func(t *T) {
				t.Log("message")
			},
			assert: func(*testing.T, *T) {
				// Delegates to the embedded testing.TB.
			},
		},
		{
			name: "Logf",
			fn: func(t *T) {
				t.Logf("message: %s", "value")
			},
			assert: func(*testing.T, *T) {
				// Delegates to the embedded testing.TB.
			},
		},
		{
			name: "Name",
			fn: func(t *T) {
				_ = t.Name()
			},
			assert: func(*testing.T, *T) {
				// Delegates to the embedded testing.TB.
			},
		},
		{
			name: "Output",
			fn: func(t *T) {
				_ = t.Output()
			},
			panicValue: "await.T.Output cannot be used inside polling conditions",
		},
		{
			name: "Setenv",
			fn: func(t *T) {
				t.Setenv("key", "value")
			},
			panicValue: "await.T.Setenv cannot be used inside polling conditions",
		},
		{
			name: "Skip",
			fn: func(t *T) {
				t.Skip("reason")
			},
			panicValue: "await.T.Skip cannot be used inside polling conditions",
		},
		{
			name: "Skipf",
			fn: func(t *T) {
				t.Skipf("reason: %s", "value")
			},
			panicValue: "await.T.Skipf cannot be used inside polling conditions",
		},
		{
			name: "SkipNow",
			fn: func(t *T) {
				t.SkipNow()
			},
			panicValue: "await.T.SkipNow cannot be used inside polling conditions",
		},
		{
			name: "Skipped",
			fn: func(t *T) {
				_ = t.Skipped()
			},
			assert: func(*testing.T, *T) {
				// Delegates to the embedded testing.TB.
			},
		},
		{
			name: "TempDir",
			fn: func(t *T) {
				_ = t.TempDir()
			},
			panicValue: "await.T.TempDir cannot be used inside polling conditions",
		},
	} {
		require.Truef(t, prevName == "" || strings.ToLower(tc.name) > strings.ToLower(prevName),
			"table entries must be alphabetical: %s before %s", prevName, tc.name)
		prevName = tc.name

		require.Contains(t, required, tc.name, "table entry must match testing.TB method")
		require.NotNil(t, tc.fn, "table entry must exercise the testing.TB method")
		require.True(t, tc.panicValue != nil || tc.assert != nil, "table entry must assert behavior")
		delete(required, tc.name)

		t.Run(tc.name, func(t *testing.T) {
			at := &T{TB: t}

			if tc.panicValue != nil {
				require.PanicsWithValue(t, tc.panicValue, func() { tc.fn(at) })
			} else {
				require.NotPanics(t, func() { tc.fn(at) })
			}
			if tc.assert != nil {
				tc.assert(t, at)
			}
		})
	}

	require.Empty(t, required, "testing.TB methods must be covered by TestT_TestingTBMethods")
}
