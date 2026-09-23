package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func wsClients(h *hub) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func wsWaitGone(h *hub, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if wsClients(h) == 0 {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func wsDial(t *testing.T, h *hub) (*websocket.Conn, context.Context) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(h.ws))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Read(ctx); err != nil { // the initial state message
		t.Fatal(err)
	}
	if n := wsClients(h); n != 1 {
		t.Fatalf("clients=%d after dial, want 1", n)
	}
	return c, ctx
}

// The browser only listens: a client that never sends a frame keeps
// receiving broadcasts (CloseRead must not disturb the write path).
func TestWSClientKeepsReceivingBroadcasts(t *testing.T) {
	h := newHub(newPoller([]RouterCfg{}, map[int]string{}, time.Second), 10)
	c, ctx := wsDial(t, h)
	defer c.CloseNow()
	for i := 0; i < 5; i++ {
		h.broadcast([]byte(fmt.Sprintf(`{"type":"event","id":%d}`, i)))
	}
	for i := 0; i < 5; i++ {
		rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
		_, b, err := c.Read(rctx)
		rcancel()
		if err != nil {
			t.Fatalf("broadcast %d not received: %v", i, err)
		}
		if !strings.Contains(string(b), `"type":"event"`) {
			t.Fatalf("unexpected message %q", b)
		}
	}
	if n := wsClients(h); n != 1 {
		t.Fatalf("clients=%d after five broadcasts, want 1", n)
	}
}

// The tab is closed: the browser sends a close frame. The handler never read
// the socket, so on a quiet fabric (no broadcast) the goroutine and the
// hijacked connection lived on — measured 2026-09-20 on the live dashboard
// as CLOSE_WAIT sockets with the close frame unread in rx_queue.
func TestWSHandlerExitsOnClientClose(t *testing.T) {
	h := newHub(newPoller([]RouterCfg{}, map[int]string{}, time.Second), 10)
	c, _ := wsDial(t, h)
	start := time.Now()
	err := c.Close(websocket.StatusNormalClosure, "bye") // returns when the peer answers the close handshake
	t.Logf("client Close returned after %s: %v", time.Since(start).Round(time.Millisecond), err)
	if !wsWaitGone(h, 3*time.Second) {
		t.Fatalf("clients=%d 3 s after the client closed; the handler never returned", wsClients(h))
	}
}

// The peer vanishes without a close frame (crash, network loss): the read
// side sees EOF and the handler must still return.
func TestWSHandlerExitsOnAbruptDisconnect(t *testing.T) {
	h := newHub(newPoller([]RouterCfg{}, map[int]string{}, time.Second), 10)
	c, _ := wsDial(t, h)
	_ = c.CloseNow()
	if !wsWaitGone(h, 3*time.Second) {
		t.Fatalf("clients=%d 3 s after the TCP connection dropped; the handler never returned", wsClients(h))
	}
}
