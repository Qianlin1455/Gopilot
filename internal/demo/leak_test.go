package demo

import (
	"bytes"
	"errors"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

func TestLeakCountBounds(t *testing.T) {
	controller := NewLeakController()
	t.Cleanup(func() { controller.Reset() })

	for _, count := range []int{-1, 0, MaxLeakBatch + 1} {
		if _, err := controller.Inject(count); !errors.Is(err, ErrInvalidLeakCount) {
			t.Fatalf("Inject(%d) error = %v, want ErrInvalidLeakCount", count, err)
		}
	}

	for injected := 0; injected < MaxActiveLeaks; injected += MaxLeakBatch {
		if _, err := controller.Inject(MaxLeakBatch); err != nil {
			t.Fatalf("fill active leaks: %v", err)
		}
	}
	if _, err := controller.Inject(1); !errors.Is(err, ErrTooManyLeaks) {
		t.Fatalf("Inject beyond active limit error = %v, want ErrTooManyLeaks", err)
	}
}

func TestLeakIncreasesGoroutineCountAndResetTerminatesIt(t *testing.T) {
	controller := NewLeakController()
	baseline := runtime.NumGoroutine()

	const count = 12
	status, err := controller.Inject(count)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if status.Active != count {
		t.Fatalf("active leaks = %d, want %d", status.Active, count)
	}
	if got := runtime.NumGoroutine(); got < baseline+count {
		t.Fatalf("goroutine count = %d, want at least %d", got, baseline+count)
	}

	status = controller.Reset()
	if status.Active != 0 {
		t.Fatalf("active leaks after reset = %d, want 0", status.Active)
	}
	waitFor(t, time.Second, func() bool {
		return runtime.NumGoroutine() <= baseline+2
	}, "goroutine count to return near baseline")
}

func TestLeakAppearsInGoroutineProfile(t *testing.T) {
	controller := NewLeakController()
	t.Cleanup(func() { controller.Reset() })

	if _, err := controller.Inject(3); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	var profile bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&profile, 2); err != nil {
		t.Fatalf("write goroutine profile: %v", err)
	}
	if !strings.Contains(profile.String(), "(*LeakController).waitForever") {
		t.Fatalf("goroutine profile does not contain the intentional leak function:\n%s", profile.String())
	}

	controller.Reset()
	waitFor(t, time.Second, func() bool {
		profile.Reset()
		_ = pprof.Lookup("goroutine").WriteTo(&profile, 2)
		return !strings.Contains(profile.String(), "(*LeakController).waitForever")
	}, "intentional leak stack to disappear after reset")
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
