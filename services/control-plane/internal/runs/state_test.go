package runs

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestFinalizationMatrix(t *testing.T) {
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	provider, ref := "docker", "lease-1"
	noResources := Cleanup{Lease: Absent, Runtime: Absent}
	tests := []struct {
		name               string
		status             Status
		phase              Phase
		provider, ref      *string
		target             Status
		cleanup            Cleanup
		finalized, illegal bool
	}{
		{"queued cancel", Queued, "", nil, nil, Cancelled, Cleanup{}, true, false},
		{"prepare failure before acquire", Running, Preparing, nil, nil, Failed, noResources, true, false},
		{"acquire definitely absent", Running, Preparing, &provider, nil, Failed, noResources, true, false},
		{"uncertain acquire", Running, Preparing, &provider, nil, Failed, Cleanup{}, false, false},
		{"zero values cannot finalize", Running, Preparing, nil, nil, Failed, Cleanup{}, false, false},
		{"execute absent waiting release", Running, Preparing, &provider, &ref, Failed, Cleanup{Lease: Present, Runtime: Absent}, false, false},
		{"execute absent released", Running, Preparing, &provider, &ref, Failed, Cleanup{Lease: Present, Runtime: Absent, Released: true}, true, false},
		{"uncertain execute", Running, Agent, &provider, &ref, Failed, Cleanup{Lease: Present, Released: true}, false, false},
		{"agent failure pending abort", Running, Agent, &provider, &ref, Failed, Cleanup{Lease: Present, Runtime: Present, Released: true}, false, false},
		{"agent failure", Running, Agent, &provider, &ref, Failed, Cleanup{Lease: Present, Runtime: Present, Disposition: Abort, Released: true}, true, false},
		{"publish failure", Running, Publishing, &provider, &ref, Failed, Cleanup{Lease: Present, Runtime: Present, Disposition: Abort, Released: true}, true, false},
		{"active cancellation", Running, Agent, &provider, &ref, Cancelled, Cleanup{Lease: Present, Runtime: Present, Disposition: Abort, Released: true}, true, false},
		{"preparing cancellation", Running, Preparing, nil, nil, Cancelled, noResources, true, false},
		{"restart interruption", Running, Agent, &provider, &ref, Interrupted, Cleanup{Lease: Present, Runtime: Present, Disposition: Abort, Released: true}, true, false},
		{"restart absent", Running, Preparing, &provider, nil, Interrupted, noResources, true, false},
		{"success pending commit", Running, Publishing, &provider, &ref, Succeeded, Cleanup{Lease: Present, Runtime: Present, Released: true}, false, false},
		{"success commit", Running, Publishing, &provider, &ref, Succeeded, Cleanup{Lease: Present, Runtime: Present, Disposition: Commit, Released: true}, true, false},
		{"runtime finalized release failed", Running, Publishing, &provider, &ref, Succeeded, Cleanup{Lease: Present, Runtime: Present, Disposition: Commit}, false, false},
		{"success reconciliation", Succeeded, Publishing, &provider, &ref, Succeeded, Cleanup{Lease: Present, Runtime: Present, Disposition: Commit, Released: true}, true, false},
		{"failed reconciliation", Failed, Agent, &provider, &ref, Failed, Cleanup{Lease: Present, Runtime: Present, Disposition: Abort, Released: true}, true, false},
		{"publishing cancellation refused", Running, Publishing, &provider, &ref, Cancelled, Cleanup{}, false, true},
		{"queued success refused", Queued, "", nil, nil, Succeeded, noResources, false, true},
		{"early success refused", Running, Agent, &provider, &ref, Succeeded, Cleanup{}, false, true},
		{"terminal status immutable", Succeeded, Publishing, &provider, &ref, Failed, Cleanup{}, false, true},
		{"nonterminal target refused", Running, Preparing, nil, nil, Queued, noResources, false, true},
		{"wrong disposition refused", Running, Publishing, &provider, &ref, Succeeded, Cleanup{Lease: Present, Runtime: Present, Disposition: Abort, Released: true}, false, true},
		{"acquired cannot become never acquired", Running, Agent, &provider, &ref, Failed, noResources, false, true},
		{"success requires execution", Running, Publishing, &provider, nil, Succeeded, noResources, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := Run{Status: tt.status, SandboxProvider: tt.provider, SandboxRef: tt.ref}
			if tt.phase != "" {
				run.Phase = &tt.phase
			}
			before := run
			got, err := Finish(run, tt.target, tt.cleanup, now)
			if !reflect.DeepEqual(run, before) {
				t.Fatal("input mutated")
			}
			if tt.illegal {
				var domain *DomainError
				if !errors.As(err, &domain) {
					t.Fatalf("want domain error, got %v", err)
				}
				return
			}
			if err != nil || got.Status != tt.target || (got.FinalizedAt != nil) != tt.finalized {
				t.Fatalf("Finish() = %#v, %v", got, err)
			}
			if got.FinalizedAt != nil && !got.FinalizedAt.Equal(now) {
				t.Fatal("wrong finalization timestamp")
			}
			if got.SandboxProvider != tt.provider || got.SandboxRef != tt.ref {
				t.Fatal("audit identity lost")
			}
		})
	}
}

func TestStateTransitions(t *testing.T) {
	now := time.Now()
	run, err := Start(Run{Status: Queued}, now)
	if err != nil || run.Status != Running || run.Phase == nil || *run.Phase != Preparing {
		t.Fatalf("Start = %#v, %v", run, err)
	}
	for _, phase := range []Phase{Agent, Publishing} {
		run, err = Advance(run, phase, now)
		if err != nil || *run.Phase != phase {
			t.Fatalf("Advance = %#v, %v", run, err)
		}
	}
	for _, phase := range []Phase{Preparing, Agent, Publishing, "invalid"} {
		if _, err := Advance(run, phase, now); err == nil {
			t.Fatalf("accepted illegal phase %s", phase)
		}
	}
	if _, err := Start(run, now); err == nil {
		t.Fatal("restarted running run")
	}
	if _, err := Advance(Run{Status: Queued}, Agent, now); err == nil {
		t.Fatal("advanced queued run")
	}
	final := now.Add(-time.Hour)
	phase := Publishing
	if _, err := Finish(Run{Status: Running, Phase: &phase, FinalizedAt: &final}, Succeeded, Cleanup{}, now); err == nil {
		t.Fatal("accepted a status change on an already finalized run")
	}
	finished := Run{Status: Cancelled, FinalizedAt: &final}
	got, err := Finish(finished, Cancelled, Cleanup{}, now)
	if err != nil || !got.FinalizedAt.Equal(final) {
		t.Fatal("finalization not idempotent")
	}
}
