// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// waitHost is a daemon with one instance's events and state, which a test
// changes as the instance runs.
type waitHost struct {
	// mu guards the instance and the events.
	mu       sync.Mutex
	instance *dicerdv1.Instance
	history  []*dicerdv1.Event

	// live carries the events that happen once the history is sent.
	live chan *dicerdv1.Event

	// read is closed the first time the instance is read.
	read     chan struct{}
	readOnce sync.Once
}

func newWaitHost(instance *dicerdv1.Instance, history ...*dicerdv1.Event) *waitHost {
	return &waitHost{instance: instance, history: history, live: make(chan *dicerdv1.Event, 1), read: make(chan struct{})}
}

// daemon returns the daemon serving the host.
func (h *waitHost) daemon() *fakeDaemon {
	return &fakeDaemon{
		getInstance: func(context.Context, *dicerdv1.GetInstanceRequest) (*dicerdv1.Instance, error) {
			defer h.readOnce.Do(func() { close(h.read) })
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.instance == nil {
				return nil, status.Error(codes.NotFound, "no instance job")
			}
			return h.instance, nil
		},
		getEvents: func(_ *dicerdv1.GetEventsRequest, stream grpc.ServerStreamingServer[dicerdv1.GetEventsResponse]) error {
			h.mu.Lock()
			history := h.history
			h.mu.Unlock()
			if err := stream.Send(&dicerdv1.GetEventsResponse{Events: history, CaughtUp: true}); err != nil {
				return err
			}
			for {
				select {
				case e := <-h.live:
					if err := stream.Send(&dicerdv1.GetEventsResponse{Events: []*dicerdv1.Event{e}}); err != nil {
						return err
					}
				case <-stream.Context().Done():
					return nil
				}
			}
		},
	}
}

// end stops the instance as having exited with code, and reports it.
func (h *waitHost) end(code int32) {
	h.mu.Lock()
	h.instance = &dicerdv1.Instance{Id: "id1", Name: "job", State: dicerdv1.InstanceState_INSTANCE_STATE_STOPPED, ExitCode: &code}
	h.mu.Unlock()
	h.live <- &dicerdv1.Event{
		Id: "id1", Name: "job", Action: dicerdv1.EventAction_EVENT_ACTION_EXITED,
		Attributes: map[string]string{"exit_code": "0"},
	}
}

// TestWaitReturnsAtOnceForAStoppedInstance checks that an instance that has
// already stopped is not waited for.
func TestWaitReturnsAtOnceForAStoppedInstance(t *testing.T) {
	code := int32(3)
	host := newWaitHost(&dicerdv1.Instance{
		Id: "id1", Name: "job", State: dicerdv1.InstanceState_INSTANCE_STATE_STOPPED, ExitCode: &code,
	})
	c := connect(t, host.daemon())

	got, err := c.Instances.Wait(t.Context(), "job", WaitOptions{})
	if err != nil || got != 3 {
		t.Errorf("Wait = %d, %v; want 3", got, err)
	}
}

// TestWaitWaitsForARunningInstanceToStop checks that a stop reported after
// the wait began ends it.
func TestWaitWaitsForARunningInstanceToStop(t *testing.T) {
	host := newWaitHost(&dicerdv1.Instance{Id: "id1", Name: "job", State: dicerdv1.InstanceState_INSTANCE_STATE_RUNNING})
	c := connect(t, host.daemon())

	go func() {
		<-host.read
		host.end(0)
	}()

	got, err := c.Instances.Wait(t.Context(), "job", WaitOptions{})
	if err != nil || got != 0 {
		t.Errorf("Wait = %d, %v; want 0", got, err)
	}
}

// TestWaitReportsADeletedInstanceFromItsEvents checks that an instance
// deleted before the wait began is reported as its events say it ended.
func TestWaitReportsADeletedInstanceFromItsEvents(t *testing.T) {
	host := newWaitHost(nil,
		&dicerdv1.Event{Id: "id1", Name: "job", Action: dicerdv1.EventAction_EVENT_ACTION_STARTED},
		&dicerdv1.Event{Id: "id1", Name: "job", Action: dicerdv1.EventAction_EVENT_ACTION_DIED},
		&dicerdv1.Event{Id: "id1", Name: "job", Action: dicerdv1.EventAction_EVENT_ACTION_DELETED},
	)
	c := connect(t, host.daemon())

	got, err := c.Instances.Wait(t.Context(), "job", WaitOptions{})
	if err != nil || got != UnknownExitCode {
		t.Errorf("Wait = %d, %v; want %d", got, err, UnknownExitCode)
	}
}

// TestWaitForAnInstanceThatNeverExistedIsNotFound checks that a name no
// instance has had is reported as not found.
func TestWaitForAnInstanceThatNeverExistedIsNotFound(t *testing.T) {
	c := connect(t, newWaitHost(nil).daemon())

	if _, err := c.Instances.Wait(t.Context(), "job", WaitOptions{}); err == nil {
		t.Error("Wait succeeded")
	}
}
