package scheduler

import (
	"math/rand"
	"time"
)

type lockedRand struct {
	src *rand.Rand
}

func newLockedRand() *lockedRand {
	return &lockedRand{src: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

func (r *lockedRand) Duration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(r.src.Int63n(int64(max)))
}
