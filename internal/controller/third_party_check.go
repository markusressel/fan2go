package controller

import "time"

// minThirdPartyConfirmationDelay is the shortest time between the read that finds a mismatch in a third party
// sanity check and the read that confirms it (see thirdPartyConfirmationDelay). Drivers like it87 cache the
// register values for 1.5 s, so a read within that time returns the same (possibly garbage) value again.
const minThirdPartyConfirmationDelay = 2 * time.Second

// confirmationResult is the outcome of a read of a third party sanity check (see pendingConfirmation.observe).
type confirmationResult int

const (
	// confirmationMatch: the read shows the expected value, and no mismatch was pending
	confirmationMatch confirmationResult = iota
	// confirmationStarted: the read shows an unexpected value for the first time, it is confirmed later
	confirmationStarted
	// confirmationGlitch: a pending mismatch didn't persist, and fan2go didn't write in between: a read glitch
	confirmationGlitch
	// confirmationOverwritten: a pending mismatch didn't persist, but fan2go wrote a PWM value in between, so it
	// can't tell whether the first read was a glitch or a third party change that the write undid
	confirmationOverwritten
	// confirmationConfirmed: a pending mismatch persisted, it is a third party change
	confirmationConfirmed
)

// pendingConfirmation is the state of a third party sanity check that confirms a mismatch with a second read
// before it reacts to it (confirm before reacting): a single garbage read (e.g. a BIOS and the kernel driver
// racing on the index/data registers of a Super I/O chip) must neither be reported nor "restored" with a write.
// The second read happens on a later update (see due), so the update loop never waits for it.
// Only used by the update loop.
type pendingConfirmation struct {
	// since is the time of the read that found the mismatch, zero if no mismatch is pending
	since time.Time
	// due is the time from which the next check reads the value again to confirm the mismatch
	due time.Time
	// written is true if fan2go wrote a PWM value since the mismatch was found (see noteWrite)
	written bool
}

// pending returns true while a mismatch waits for its confirmation.
func (p *pendingConfirmation) pending() bool {
	return !p.since.IsZero()
}

// waiting returns true while a mismatch is pending and its confirmation is not due yet at now: the check must not
// read the value then (a caching driver would return the same value again).
func (p *pendingConfirmation) waiting(now time.Time) bool {
	return p.pending() && now.Before(p.due)
}

// noteWrite records that fan2go wrote a PWM value, if a mismatch is pending.
func (p *pendingConfirmation) noteWrite() {
	if p.pending() {
		p.written = true
	}
}

// clear drops a pending mismatch.
func (p *pendingConfirmation) clear() {
	*p = pendingConfirmation{}
}

// observe evaluates a read of the check at now: matches is true if it shows the expected value. A first mismatch
// becomes pending for delay; a read after that resolves it. Must not be called while waiting returns true.
func (p *pendingConfirmation) observe(now time.Time, delay time.Duration, matches bool) confirmationResult {
	if !p.pending() {
		if matches {
			return confirmationMatch
		}
		*p = pendingConfirmation{since: now, due: now.Add(delay)}
		return confirmationStarted
	}
	written := p.written
	p.clear()
	switch {
	case !matches:
		return confirmationConfirmed
	case written:
		return confirmationOverwritten
	default:
		return confirmationGlitch
	}
}

// thirdPartyConfirmationDelay returns the time a third party sanity check waits before it reads a value again to
// confirm a mismatch: the measured readback delay of the fan or pwmSetDelay, but at least
// minThirdPartyConfirmationDelay, so the second read comes from a fresh update of a caching driver.
func (f *DefaultFanController) thirdPartyConfirmationDelay() time.Duration {
	return max(f.readbackDelay, f.getPwmSetDelay(), minThirdPartyConfirmationDelay)
}

// clearPendingThirdPartyChecks drops the pending mismatches of the third party sanity checks, e.g. when fan2go
// hands the fan back: the values it reads afterwards are not compared with what it found before.
func (f *DefaultFanController) clearPendingThirdPartyChecks() {
	f.pendingPwmMismatch.clear()
	f.pendingModeMismatch.clear()
}
