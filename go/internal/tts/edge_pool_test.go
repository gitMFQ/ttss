package tts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeEdge starts an in-process WebSocket server that behaves like the Edge
// TTS endpoint: it answers every ssml frame with one audio frame + turn.end.
type fakeEdge struct {
	server *httptest.Server

	mu       sync.Mutex
	accepted int
	turns    int
	// delay is applied before answering each turn.
	delay time.Duration
	// silent makes the server accept turns but never answer them.
	silent bool
	// failFirst drops the socket (without answering) for the first N turns
	// seen across all connections, exercising the retry path.
	failFirst int
}

func newFakeEdge(t *testing.T) *fakeEdge {
	t.Helper()
	f := &fakeEdge{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		f.mu.Lock()
		f.accepted++
		f.mu.Unlock()

		for {
			kind, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if kind != websocket.TextMessage {
				continue
			}
			text := string(msg)
			idx := strings.Index(text, "X-RequestId:")
			if idx < 0 {
				continue // speech.config frame
			}
			rid := text[idx+len("X-RequestId:"):]
			if i := strings.IndexAny(rid, "\r\n"); i >= 0 {
				rid = rid[:i]
			}
			rid = strings.TrimSpace(rid)

			f.mu.Lock()
			delay, silent, failFirst := f.delay, f.silent, f.failFirst
			f.turns++
			n := f.turns
			f.mu.Unlock()

			if failFirst > 0 && n <= failFirst {
				// Kill the socket without answering so the turn never lands.
				return
			}
			if silent {
				continue
			}
			if delay > 0 {
				time.Sleep(delay)
			}
			if err := writeEdgeTurn(conn, rid, []byte("AUDIO-"+rid)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func writeEdgeTurn(conn *websocket.Conn, rid string, payload []byte) error {
	hdr := fmt.Sprintf("X-RequestId:%s\r\nContent-Type:audio/mpeg\r\nPath:audio\r\n", rid)
	frame := make([]byte, 2+len(hdr)+len(payload))
	frame[0] = byte(len(hdr) >> 8)
	frame[1] = byte(len(hdr))
	copy(frame[2:], hdr)
	copy(frame[2+len(hdr):], payload)
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return err
	}
	end := fmt.Sprintf("X-RequestId:%s\r\nContent-Type:application/json\r\nPath:turn.end\r\n\r\n{}", rid)
	return conn.WriteMessage(websocket.TextMessage, []byte(end))
}

func (f *fakeEdge) url() string { return "ws" + strings.TrimPrefix(f.server.URL, "http") }

func (f *fakeEdge) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accepted
}

// newTestPool returns a pool whose dialer hands out connections to the fake.
func newTestPool(t *testing.T, f *fakeEdge, cfg edgePoolConfig) *edgePool {
	t.Helper()
	dialer := &websocketDialer{
		handshakeTO: 5 * time.Second,
		dialEdgeOverride: func() (*websocket.Conn, error) {
			conn, _, err := websocket.DefaultDialer.Dial(f.url(), nil)
			return conn, err
		},
	}
	return newEdgePool(dialer, cfg)
}

func submitText(t *testing.T, pool *edgePool, text string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rid, err := randomHex(8)
	if err != nil {
		t.Fatal(err)
	}
	task := newEdgeTask(ctx, rid, text, Options{Voice: "v", Volume: 100})
	select {
	case r := <-pool.submit(task):
		return r.audio, r.err
	case <-time.After(10 * time.Second):
		t.Fatal("submit never completed")
		return nil, nil
	}
}

// A turn that finishes must release the slot immediately; before the fix the
// slot stayed busy for the full turn timeout, capping throughput at one
// request per timeout per slot.
func TestPoolReleasesSlotAfterEachTurn(t *testing.T) {
	f := newFakeEdge(t)
	pool := newTestPool(t, f, edgePoolConfig{poolSize: 1, turnTimeout: 60 * time.Second})
	defer pool.shutdown()

	start := time.Now()
	for i := 0; i < 3; i++ {
		audio, err := submitText(t, pool, fmt.Sprintf("hello %d", i))
		if err != nil {
			t.Fatalf("turn %d failed: %v", i, err)
		}
		if len(audio) == 0 {
			t.Fatalf("turn %d returned empty audio", i)
		}
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("3 sequential turns took %v; slot is not released between turns", elapsed)
	}
	if got := f.connections(); got != 1 {
		t.Fatalf("expected the single slot to reuse one connection, dialled %d", got)
	}
}

// A failed send must not leave the slot wedged.
func TestPoolSurvivesConnectionLoss(t *testing.T) {
	f := newFakeEdge(t)
	f.failFirst = 1
	pool := newTestPool(t, f, edgePoolConfig{poolSize: 1, turnTimeout: 5 * time.Second})
	defer pool.shutdown()

	_, _ = submitText(t, pool, "first")
	audio, err := submitText(t, pool, "second")
	if err != nil {
		t.Fatalf("second turn after connection loss failed: %v", err)
	}
	if len(audio) == 0 {
		t.Fatal("second turn returned empty audio")
	}
	if got := f.connections(); got < 2 {
		t.Fatalf("expected a reconnect after the drop, saw %d connection(s)", got)
	}
}

// A turn whose socket dies before it is answered must be replayed once on a
// fresh connection instead of surfacing a spurious error.
func TestPoolRetriesTurnLostBeforeAnswer(t *testing.T) {
	f := newFakeEdge(t)
	f.failFirst = 1
	pool := newTestPool(t, f, edgePoolConfig{poolSize: 1, turnTimeout: 5 * time.Second})
	defer pool.shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rid, err := randomHex(8)
	if err != nil {
		t.Fatal(err)
	}
	task := newEdgeTask(ctx, rid, "retry me", Options{Voice: "v", Volume: 100})
	r := <-pool.submit(task)
	if r.err != nil {
		t.Fatalf("expected the retry to succeed, got %v", r.err)
	}
	if len(r.audio) == 0 {
		t.Fatal("retry returned empty audio")
	}
	if !task.retried {
		t.Fatal("expected the task to be retried")
	}
}

// A turn that never completes must fail with the timeout error, not hang, and
// the next turn must still work.
func TestPoolTimesOutStalledTurn(t *testing.T) {
	f := newFakeEdge(t)
	f.silent = true
	pool := newTestPool(t, f, edgePoolConfig{poolSize: 1, turnTimeout: 300 * time.Millisecond})
	defer pool.shutdown()

	start := time.Now()
	_, err := submitText(t, pool, "stall")
	if !errors.Is(err, errEdgeTimeout) {
		t.Fatalf("expected errEdgeTimeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %v, expected ~300ms", elapsed)
	}

	// Recover: a fresh connection that answers must succeed.
	f.mu.Lock()
	f.silent = false
	f.mu.Unlock()
	audio, err := submitText(t, pool, "recover")
	if err != nil {
		t.Fatalf("turn after timeout failed: %v", err)
	}
	if len(audio) == 0 {
		t.Fatal("turn after timeout returned empty audio")
	}
}

// Cancelling the caller's context must release the slot instead of pinning it
// for the full timeout.
func TestPoolHonoursContextCancellation(t *testing.T) {
	f := newFakeEdge(t)
	f.silent = true
	pool := newTestPool(t, f, edgePoolConfig{poolSize: 1, turnTimeout: 30 * time.Second})
	defer pool.shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	rid, _ := randomHex(8)
	task := newEdgeTask(ctx, rid, "cancel me", Options{Voice: "v", Volume: 100})
	ch := pool.submit(task)
	time.AfterFunc(100*time.Millisecond, cancel)

	select {
	case r := <-ch:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled turn did not complete")
	}
}

// Concurrent turns on a small pool must all complete.
func TestPoolHandlesConcurrentTurns(t *testing.T) {
	f := newFakeEdge(t)
	pool := newTestPool(t, f, edgePoolConfig{poolSize: 2, turnTimeout: 10 * time.Second})
	defer pool.shutdown()

	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := submitText(t, pool, fmt.Sprintf("text %d", i)); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent turn failed: %v", err)
	}
}

// deliver must be idempotent: a late frame after a timeout must not panic or
// block the read loop.
func TestEdgeTaskDeliverIsIdempotent(t *testing.T) {
	task := newEdgeTask(context.Background(), "rid", "t", Options{})
	task.deliver(edgeTaskResult{audio: []byte("first")})
	task.deliver(edgeTaskResult{err: errors.New("second")})
	select {
	case r := <-task.result:
		if string(r.audio) != "first" || r.err != nil {
			t.Fatalf("first delivery should win, got %+v", r)
		}
	default:
		t.Fatal("no result delivered")
	}
	select {
	case extra := <-task.result:
		t.Fatalf("unexpected second delivery: %+v", extra)
	default:
	}
}

func TestRedactProxyHidesCredentials(t *testing.T) {
	if got := redactProxy("http://user:secret@127.0.0.1:7890"); strings.Contains(got, "secret") || strings.Contains(got, "user") {
		t.Fatalf("credentials leaked: %s", got)
	}
	if got := redactProxy("http://127.0.0.1:7890"); got != "http://127.0.0.1:7890" {
		t.Fatalf("unexpected redaction: %s", got)
	}
	if got := redactProxy(""); got != "" {
		t.Fatalf("empty proxy should stay empty, got %q", got)
	}
}
