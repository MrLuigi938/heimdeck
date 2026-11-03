package integrations

import (
	"errors"
	"testing"
	"time"
)

func TestRememberKeepsLastGoodResult(t *testing.T) {
	in := &Instance{}
	key := "test-remember-" + time.Now().String()
	calls := 0
	fail := errors.New("overloaded")
	get := func(res int, err error) (int, error) {
		return remember(in, key, time.Millisecond, func() (int, error) { calls++; return res, err })
	}
	if v, err := get(1, nil); v != 1 || err != nil {
		t.Fatalf("first: %v %v", v, err)
	}
	time.Sleep(2 * time.Millisecond)
	// The source fails: the old result stays, without an error.
	if v, err := get(0, fail); v != 1 || err != nil {
		t.Fatalf("failure should keep 1: %v %v", v, err)
	}
	// During the pause the source is not asked again.
	if v, err := get(2, nil); v != 1 || err != nil || calls != 2 {
		t.Fatalf("pause: %v %v calls=%d", v, err, calls)
	}

	// Without an earlier result the error is kept for the pause, too.
	key2 := key + "-2"
	calls = 0
	for i := 0; i < 3; i++ {
		if _, err := remember(in, key2, time.Minute, func() (int, error) { calls++; return 0, fail }); err != fail {
			t.Fatalf("want the error, got %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("failing source asked %d times, want 1", calls)
	}
}
