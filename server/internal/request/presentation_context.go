package request

import (
	"context"
	"sync"
)

type presentationContextKey struct{}
type nativePresentationRead struct {
	done  chan struct{}
	value any
	err   error
}
type nativePresentationReads struct {
	mu      sync.Mutex
	entries map[string]*nativePresentationRead
}

// PresentationContext shares native reads only within this one authorized
// response. Calendar rows and download children can refer to the same series;
// fetching its episode list again for every label would multiply provider work.
func PresentationContext(ctx context.Context) context.Context {
	if ctx.Value(presentationContextKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, presentationContextKey{}, &nativePresentationReads{entries: map[string]*nativePresentationRead{}})
}

func presentationRead[T any](ctx context.Context, key string, fetch func() (T, error)) (T, error) {
	reads, _ := ctx.Value(presentationContextKey{}).(*nativePresentationReads)
	if reads == nil {
		return fetch()
	}
	reads.mu.Lock()
	current, exists := reads.entries[key]
	if !exists {
		current = &nativePresentationRead{done: make(chan struct{})}
		reads.entries[key] = current
	}
	reads.mu.Unlock()
	if !exists {
		value, err := fetch()
		current.value, current.err = value, err
		close(current.done)
	}
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case <-current.done:
	}
	value, _ := current.value.(T)
	return value, current.err
}
