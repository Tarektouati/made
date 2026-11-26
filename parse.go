package main

import (
	"fmt"
	"time"
)

// parseTimeoutError reports that parsing took longer than the given limit.
type parseTimeoutError struct{ limit time.Duration }

func (e parseTimeoutError) Error() string {
	return fmt.Sprintf("parsing took longer than %v", e.limit)
}

// withinParseTimeout runs parse and returns its result, or a parseTimeoutError if it
// does not finish within limit. goldmark cannot be cancelled, so a parse that
// times out keeps running in its goroutine until made exits.
func withinParseTimeout[T any](limit time.Duration, parse func() T) (T, error) {
	done := make(chan T, 1)
	go func() { done <- parse() }()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case v := <-done:
		return v, nil
	case <-timer.C:
		var zero T
		return zero, parseTimeoutError{limit}
	}
}
