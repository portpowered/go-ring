package replay_test

import (
	"sync"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/signaling"
)

type recordedAlarm struct {
	when time.Time
	ch   chan time.Time
}

type recordedClock struct {
	mu     sync.Mutex
	now    time.Time
	alarms []recordedAlarm
}

func newRecordedClock() *recordedClock {
	return &recordedClock{now: time.Unix(1700000000, 0)}
}

func (c *recordedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *recordedClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.alarms = append(c.alarms, recordedAlarm{when: c.now.Add(d), ch: ch})
	return ch
}

func (c *recordedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	remaining := c.alarms[:0]
	for _, alarm := range c.alarms {
		if !alarm.when.After(c.now) {
			alarm.ch <- c.now
		} else {
			remaining = append(remaining, alarm)
		}
	}
	c.alarms = remaining
}

func recordedNextMessage(t *testing.T, out <-chan signaling.Message) signaling.Message {
	t.Helper()
	select {
	case message := <-out:
		return message
	case <-time.After(time.Second):
		t.Fatal("no outbound signaling message")
		return signaling.Message{}
	}
}
