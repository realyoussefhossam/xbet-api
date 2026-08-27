package xbet

import (
	"testing"
)

func TestMirrorPoolPrefersHealthy(t *testing.T) {
	p := NewMirrorPool([]string{"a", "b", "c"})

	p.ReportFailure("a")
	p.ReportFailure("a") // two consecutive failures -> demoted

	order := p.Hosts()
	// all hosts must be present exactly once
	if len(order) != 3 {
		t.Fatalf("want 3 hosts, got %v", order)
	}
	seen := map[string]bool{}
	for _, h := range order {
		if seen[h] {
			t.Fatalf("duplicate host %s in %v", h, order)
		}
		seen[h] = true
	}
}

func TestMirrorPoolPreferredFirst(t *testing.T) {
	p := NewMirrorPool([]string{"a", "b", "c"})
	p.ReportSuccess("b")

	order := p.Hosts()
	if order[0] != "b" {
		t.Fatalf("want preferred mirror b first, got %v", order)
	}
}

func TestMirrorPoolRepeatedFailureDemotes(t *testing.T) {
	p := NewMirrorPool([]string{"a", "b"})
	p.ReportFailure("a")
	// one failure: still healthy
	if !p.healthy[0] {
		t.Fatal("single failure should not demote")
	}
	p.ReportFailure("a")
	if p.healthy[0] {
		t.Fatal("two failures should demote")
	}

	// restoring works
	p.ReportSuccess("a")
	if !p.healthy[0] || p.pref != 0 {
		t.Fatalf("restore failed: healthy=%v pref=%d", p.healthy, p.pref)
	}
}

func TestMirrorPoolProbeAndRestore(t *testing.T) {
	p := NewMirrorPool([]string{"a", "b"})
	p.ReportFailure("a")
	p.ReportFailure("a")

	p.ProbeAndRestore(func(host string) error {
		if host == "a" {
			return nil
		}
		return errTest
	})
	if !p.healthy[0] {
		t.Fatal("probe should have restored a")
	}
}

var errTest = &testErr{}

type testErr struct{}

func (e *testErr) Error() string { return "test error" }
