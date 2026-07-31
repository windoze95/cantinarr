package ai

import (
	"context"
	"strconv"
	"sync"
	"time"
)

const (
	maxConcurrentAIChats       = 16
	maxConcurrentSharedAIChats = 4
)

type chatAdmission struct {
	mu     sync.Mutex
	actors map[string]struct{}
	global chan struct{}
	shared chan struct{}
}

type chatAdmissionResult int

const (
	chatAdmitted chatAdmissionResult = iota
	chatActorBusy
	chatCapacityBusy
)

func newChatAdmission() *chatAdmission {
	return &chatAdmission{
		actors: make(map[string]struct{}),
		global: make(chan struct{}, maxConcurrentAIChats),
		shared: make(chan struct{}, maxConcurrentSharedAIChats),
	}
}

func (a *chatAdmission) tryAcquire(actorID int64, source string) (func(), chatAdmissionResult) {
	return a.tryAcquireKey("user:"+strconv.FormatInt(actorID, 10), source)
}

func (a *chatAdmission) tryAcquireKey(actorKey, source string) (func(), chatAdmissionResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if actorKey == "" {
		return nil, chatActorBusy
	}
	if _, active := a.actors[actorKey]; active {
		return nil, chatActorBusy
	}
	select {
	case a.global <- struct{}{}:
	default:
		return nil, chatCapacityBusy
	}
	shared := source == aiSourceShared
	if shared {
		select {
		case a.shared <- struct{}{}:
		default:
			<-a.global
			return nil, chatCapacityBusy
		}
	}
	a.actors[actorKey] = struct{}{}
	var once sync.Once
	release := func() {
		once.Do(func() {
			a.mu.Lock()
			delete(a.actors, actorKey)
			<-a.global
			if shared {
				<-a.shared
			}
			a.mu.Unlock()
		})
	}
	return release, chatAdmitted
}

// acquireKey waits for a bounded provider slot without bypassing the same
// global/shared budgets used by interactive chat. Autonomous work can wait
// inside its existing wall-clock context instead of turning a transient busy
// period into a terminal remediation failure.
func (a *chatAdmission) acquireKey(ctx context.Context, actorKey, source string) (func(), error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if release, result := a.tryAcquireKey(actorKey, source); result == chatAdmitted {
			return release, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
