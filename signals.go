package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
)

// signalReceived is the cancellation cause of a notifySignal context.
type signalReceived struct{ sig os.Signal }

func (s signalReceived) Error() string { return s.sig.String() + " signal received" }

// Signal reports the signal to runner.Run, which tears a task down differently when the
// terminal itself sent it.
func (s signalReceived) Signal() os.Signal { return s.sig }

// notifySignal is signal.NotifyContext that also remembers which signal arrived and
// lets a second one force teardown; see watchSignals. stop also releases the signal
// handler.
func notifySignal(sigs ...os.Signal) (ctx context.Context, force <-chan struct{}, stop func(), received func() os.Signal) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, sigs...)
	ctx, force, unwatch, received := watchSignals(ch)
	return ctx, force, func() { signal.Stop(ch); unwatch() }, received
}

// watchSignals cancels ctx on the first value from ch, and received reports that
// signal (nil if none came). force is closed by a second value, so a second Ctrl-C can
// skip the task's grace period. stop releases the watching goroutine and ctx; it is
// safe to call more than once.
func watchSignals(ch <-chan os.Signal) (ctx context.Context, force <-chan struct{}, stop func(), received func() os.Signal) {
	ctx, cancel := context.WithCancelCause(context.Background())
	forceCh := make(chan struct{})
	quit := make(chan struct{})
	go func() {
		select {
		case s := <-ch:
			cancel(signalReceived{s})
		case <-quit:
			return
		}
		select {
		case <-ch:
			close(forceCh)
		case <-quit:
		}
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			close(quit)
			cancel(nil)
		})
	}
	received = func() os.Signal {
		var s signalReceived
		if errors.As(context.Cause(ctx), &s) {
			return s.sig
		}
		return nil
	}
	return ctx, forceCh, stop, received
}
