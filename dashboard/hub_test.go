package main

import (
	"sync"
	"testing"
	"time"
)

// A slow client's buffer is full; broadcast (close+delete) and unregister
// race under -race. A double close or a send on a closed channel panics.
func TestBroadcastSlowClientNoDoubleClose(t *testing.T) {
	for round := 0; round < 200; round++ {
		h := newHub(nil, 10)
		cl := &wsClient{send: make(chan []byte, 8)}
		h.register(cl)
		for i := 0; i < 8; i++ {
			h.broadcast([]byte("fill"))
		}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(2)
			go func() { defer wg.Done(); h.broadcast([]byte("overflow")) }()
			go func() { defer wg.Done(); h.unregister(cl) }()
		}
		wg.Wait()
		h.mu.Lock()
		_, still := h.clients[cl]
		h.mu.Unlock()
		if still {
			t.Fatalf("round %d: slow client still registered after overflow", round)
		}
		n := 0
		closed := false
		for !closed && n < 9 {
			select {
			case _, ok := <-cl.send:
				if !ok {
					closed = true
				} else {
					n++
				}
			default:
				closed = true // open and drained: unregister won every race
			}
		}
		if n > 8 {
			t.Fatalf("round %d: %d messages buffered on an 8-slot channel", round, n)
		}
	}
}

// The ring (/api/events) and the socket frame are one object: both carry
// Type "event". addEvent used to store the event before Type was set.
func TestRingEventsCarryType(t *testing.T) {
	f := newFakeRouter(t)
	f.set(summaryWith(map[string]string{"10.200.1.3": "Established"}), "")
	p := newPoller([]RouterCfg{{Name: "leaf1", URL: f.srv.URL}}, map[int]string{65100: "spine", 65101: "leaf1"}, 2*time.Second)
	h := newHub(p, 10)
	h.runTick()
	h.mu.Lock()
	ev := h.eventsSince(0)
	h.mu.Unlock()
	if len(ev) == 0 {
		t.Fatal("no events after the first tick")
	}
	for _, e := range ev {
		if e.Type != "event" {
			t.Fatalf("ring event %d has type %q; the socket sends the same object with type \"event\"", e.ID, e.Type)
		}
	}
}
