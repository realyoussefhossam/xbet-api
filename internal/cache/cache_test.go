package cache

import (
	"testing"
	"time"
)

func TestCacheGetSet(t *testing.T) {
	c := New(time.Minute)
	c.Set("k", "v")
	v, ok := c.Get("k")
	if !ok || v != "v" {
		t.Fatalf("want v, got %v (ok=%v)", v, ok)
	}
}

func TestCacheExpiry(t *testing.T) {
	c := New(20 * time.Millisecond)
	c.Set("k", "v")
	time.Sleep(40 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("entry should have expired")
	}
}

func TestCacheGetOrLoad(t *testing.T) {
	c := New(time.Minute)
	calls := 0
	load := func() (any, error) {
		calls++
		return "loaded", nil
	}
	v, err := c.GetOrLoad("k", load)
	if err != nil || v != "loaded" {
		t.Fatalf("first load failed: %v %v", v, err)
	}
	v, err = c.GetOrLoad("k", load)
	if err != nil || v != "loaded" {
		t.Fatalf("second load failed: %v %v", v, err)
	}
	if calls != 1 {
		t.Fatalf("load called %d times, want 1", calls)
	}
}

func TestCacheDoesNotCacheErrors(t *testing.T) {
	c := New(time.Minute)
	calls := 0
	load := func() (any, error) {
		calls++
		if calls == 1 {
			return nil, errBoom
		}
		return "ok", nil
	}
	if _, err := c.GetOrLoad("k", load); err == nil {
		t.Fatal("want error")
	}
	v, err := c.GetOrLoad("k", load)
	if err != nil || v != "ok" {
		t.Fatalf("second call should retry: %v %v", v, err)
	}
}

var errBoom = &boomErr{}

type boomErr struct{}

func (e *boomErr) Error() string { return "boom" }
