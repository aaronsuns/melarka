package auth

import (
	"errors"
	"time"
)

// ErrBusy means the process-wide argon2id verification semaphore had no
// free slot within slotWait. ErrThrottled means the calling username has
// failed enough recent attempts that Login refuses to run argon2id at all.
// Both are distinct from ErrBadCredentials so the handler can answer 429
// instead of 401.
var (
	ErrBusy      = errors.New("too many login attempts, try again shortly")
	ErrThrottled = errors.New("too many failed attempts, try again shortly")
)

const (
	// verifySlotCount bounds concurrent argon2id verifications process-wide.
	// Each hash costs 19 MiB and real CPU time (OWASP argon2id baseline); a
	// burst of login attempts without this bound could exhaust memory or
	// starve every other request on the box.
	verifySlotCount = 4

	maxConsecutiveFails = 5
	failWindowSeconds   = 30
	failMapCap          = 1000
	failMapMaxAgeSecs   = 10 * 60
)

// slotWait is how long Login waits for a free verification slot before
// giving up. A var (not const) so a test can shrink it.
var slotWait = 2 * time.Second

// verifySlots is process-wide (shared by every *Store): the memory/CPU cost
// of argon2id is a property of the machine, not of any one Store instance.
var verifySlots = make(chan struct{}, verifySlotCount)

// verify is the password-verification function Login and CheckPassword call
// through, as a var so tests can substitute a slow/blocking verifier to
// exercise the semaphore deterministically instead of racing real argon2id
// timing.
var verify = VerifyPassword

// acquireVerifySlot blocks until a verification slot is free or slotWait
// elapses, returning false in the latter case.
func acquireVerifySlot() bool {
	select {
	case verifySlots <- struct{}{}:
		return true
	case <-time.After(slotWait):
		return false
	}
}

func releaseVerifySlot() { <-verifySlots }

// loginFails tracks one username's consecutive login failures.
type loginFails struct {
	count    int
	lastFail int64 // unix seconds, via Store.now()
}

// throttled reports whether key (a normalized username) has failed enough
// consecutive times, recently enough, that Login must refuse without
// running argon2id at all.
func (s *Store) throttled(key string) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	f, ok := s.fails[key]
	if !ok {
		return false
	}
	return f.count >= maxConsecutiveFails && s.now()-f.lastFail < failWindowSeconds
}

// recordFailure records one more consecutive failure for key. Called for
// both "no such user" and "wrong password" so a username-enumeration attempt
// against a nonexistent account is throttled the same way.
func (s *Store) recordFailure(key string) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	if s.fails == nil {
		s.fails = map[string]loginFails{}
	}
	f := s.fails[key]
	f.count++
	f.lastFail = s.now()
	s.fails[key] = f
	s.pruneFailsLocked()
}

// recordSuccess resets key's failure counter: a successful login proves the
// caller isn't the attacker the backoff exists for.
func (s *Store) recordSuccess(key string) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	delete(s.fails, key)
}

// pruneFailsLocked keeps the failure map from growing without bound under a
// sustained distributed attempt against many usernames: once it's grown past
// failMapCap entries, drop everything old enough that it's no longer inside
// anyone's throttle window anyway. Caller must hold s.failMu.
func (s *Store) pruneFailsLocked() {
	if len(s.fails) <= failMapCap {
		return
	}
	now := s.now()
	for k, f := range s.fails {
		if now-f.lastFail > failMapMaxAgeSecs {
			delete(s.fails, k)
		}
	}
}
