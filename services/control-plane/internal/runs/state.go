package runs

import "time"

// Presence is authoritative evidence, not a guess from an unsuccessful RPC.
// The zero value deliberately means unknown, never absent.
type Presence uint8

const (
	Unknown Presence = iota
	Absent
	Present
)

type Disposition string

const (
	Abort  Disposition = "abort"
	Commit Disposition = "commit"
)

// Cleanup contains acknowledged results. Lease=Absent means never acquired;
// an acquired-and-released lease remains Present with Released=true.
// Disposition is set only after Runtime acknowledges finalize(commit/abort).
type Cleanup struct {
	Lease, Runtime Presence
	Disposition    Disposition
	Released       bool
}

func Start(run Run, now time.Time) (Run, error) {
	if run.Status != Queued || run.FinalizedAt != nil {
		return run, ErrConflict
	}
	phase := Preparing
	run.Status, run.Phase, run.UpdatedAt = Running, &phase, now
	return run, nil
}

func Advance(run Run, phase Phase, now time.Time) (Run, error) {
	if run.Status != Running || run.FinalizedAt != nil || run.Phase == nil ||
		!((*run.Phase == Preparing && phase == Agent) || (*run.Phase == Agent && phase == Publishing)) {
		return run, ErrConflict
	}
	run.Phase, run.UpdatedAt = &phase, now
	return run, nil
}

// Finish records a product outcome independently of cleanup. Repeating the
// same outcome may acknowledge cleanup; changing a terminal outcome is forbidden.
func Finish(run Run, status Status, cleanup Cleanup, now time.Time) (Run, error) {
	if !terminal(status) || (run.Status != Running && run.Status != Queued && run.Status != status) || (run.FinalizedAt != nil && run.Status != status) {
		return run, ErrConflict
	}
	if run.Status == Queued {
		if status != Cancelled || run.SandboxProvider != nil || run.SandboxRef != nil {
			return run, ErrConflict
		}
		run.Status, run.FinalizedAt, run.UpdatedAt = status, &now, now
		return run, nil
	}
	if run.FinalizedAt != nil {
		return run, nil
	}
	if run.Phase == nil || (*run.Phase != Preparing && *run.Phase != Agent && *run.Phase != Publishing) {
		return run, ErrConflict
	}
	if status == Cancelled && *run.Phase == Publishing {
		return run, ErrConflict
	}
	if status == Succeeded && (*run.Phase != Publishing || cleanup.Lease == Absent || cleanup.Runtime == Absent) {
		return run, ErrConflict
	}
	if cleanup.Lease > Present || cleanup.Runtime > Present ||
		(cleanup.Lease == Absent && (run.SandboxRef != nil || cleanup.Runtime == Present)) ||
		(cleanup.Disposition != "" && cleanup.Disposition != Abort && cleanup.Disposition != Commit) ||
		(cleanup.Disposition == Commit && status != Succeeded) ||
		(cleanup.Disposition == Abort && status == Succeeded) {
		return run, ErrConflict
	}
	run.Status, run.UpdatedAt = status, now
	runtimeDone := cleanup.Runtime == Absent || (cleanup.Runtime == Present && cleanup.Disposition != "")
	leaseDone := cleanup.Lease == Absent || (cleanup.Lease == Present && cleanup.Released)
	if runtimeDone && leaseDone {
		run.FinalizedAt = &now
	}
	return run, nil
}

func terminal(status Status) bool {
	return status == Succeeded || status == Failed || status == Cancelled || status == Interrupted
}
