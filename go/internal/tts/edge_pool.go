package tts

import (
	"context"
	"net/url"
	"sync"
	"time"
)

// edgePoolStats is a small snapshot of pool state for observability.
type edgePoolStats struct {
	slots       int
	aliveSlots  int
	totalQueued int
}

// edgeTask is one synthesis request travelling through the pool.
//
// Ownership rules keep it race-free without extra locks:
//   - ctx/text/options are written once before submit and only read after;
//   - requestID, timer and retried are touched only by the slot that owns the
//     task, always under edgeSlot.mu;
//   - result is written through deliver, which is idempotent and safe from any
//     goroutine.
//
// Per-attempt audio lives in the connection's pendingTurn rather than here, so
// a retry never mutates state that a live connection might still be reading.
type edgeTask struct {
	ctx       context.Context
	requestID string
	text      string
	options   Options

	result      chan edgeTaskResult
	deliverOnce sync.Once

	timer   *time.Timer
	retried bool
}

type edgeTaskResult struct {
	audio []byte
	err   error
}

// newEdgeTask builds a turn. The per-request budget is armed by the slot on
// submit so that time spent queued counts against it.
func newEdgeTask(ctx context.Context, requestID, text string, options Options) *edgeTask {
	if ctx == nil {
		ctx = context.Background()
	}
	return &edgeTask{
		ctx:       ctx,
		requestID: requestID,
		text:      text,
		options:   options,
		result:    make(chan edgeTaskResult, 1),
	}
}

// deliver hands the final outcome to the caller exactly once. Later calls are
// ignored, so a late timeout can never race with a successful turn.
func (t *edgeTask) deliver(r edgeTaskResult) {
	t.deliverOnce.Do(func() {
		t.result <- r
	})
}

// ---------------------------------------------------------------------------
// Slot: one long-lived WebSocket plus a FIFO queue of turns.
// ---------------------------------------------------------------------------

// edgeSlot owns one persistent WebSocket and a FIFO queue of pending turns.
// Each turn completes (turn.end) before the next is sent, matching the
// upstream behaviour where the server processes requests in order.
type edgeSlot struct {
	index        int
	dialer       *websocketDialer
	pingInterval time.Duration
	turnTimeout  time.Duration

	mu         sync.Mutex
	conn       *edgeConn
	queue      []*edgeTask
	busy       bool
	lastUsedAt time.Time
	closing    bool
}

func newEdgeSlot(index int, dialer *websocketDialer, cfg edgePoolConfig) *edgeSlot {
	return &edgeSlot{
		index:        index,
		dialer:       dialer,
		pingInterval: cfg.pingInterval,
		turnTimeout:  cfg.turnTimeout,
		lastUsedAt:   time.Now(),
	}
}

// submit queues a turn and returns its result channel.
func (s *edgeSlot) submit(task *edgeTask) <-chan edgeTaskResult {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		task.deliver(edgeTaskResult{err: errEdgePoolClosing})
		return task.result
	}
	if err := task.ctx.Err(); err != nil {
		s.mu.Unlock()
		task.deliver(edgeTaskResult{err: err})
		return task.result
	}
	task.timer = time.NewTimer(s.turnTimeout)
	s.queue = append(s.queue, task)
	s.tryDrain()
	s.mu.Unlock()
	return task.result
}

// tryDrain pops the head of the queue, discarding tasks that were cancelled or
// ran out of budget while waiting, and hands the first live one to processTurn.
// Must be called with s.mu held.
func (s *edgeSlot) tryDrain() {
	for !s.busy && len(s.queue) > 0 {
		task := s.queue[0]
		s.queue = s.queue[1:]

		if err := task.ctx.Err(); err != nil {
			task.deliver(edgeTaskResult{err: err})
			continue
		}
		if !s.openConn() {
			task.deliver(edgeTaskResult{err: errEdgeConnLost})
			continue
		}

		s.busy = true
		go s.processTurn(task)
		return
	}
}

// openConn dials if there is no live connection. Returns false on failure.
// Must be called with s.mu held.
func (s *edgeSlot) openConn() bool {
	if s.conn != nil {
		if !s.conn.isClosed() {
			return true
		}
		s.conn.close()
		s.conn = nil
	}
	ws, err := s.dialer.dialEdge()
	if err != nil {
		return false
	}
	s.conn = newEdgeConn(ws, s.pingInterval)
	return true
}

// processTurn runs a single turn: send the frames, then wait for turn.end,
// the per-request budget, or cancellation. It always releases the slot.
func (s *edgeSlot) processTurn(task *edgeTask) {
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.lastUsedAt = time.Now()
		s.tryDrain()
		s.mu.Unlock()
	}()

	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		task.deliver(edgeTaskResult{err: errEdgeConnLost})
		return
	}

	// The connection is the only writer of attempt, and it is abandoned rather
	// than reused when we retry, so the two never share mutable state.
	attempt, err := conn.sendTurn(task)
	if err != nil {
		s.dropConn(conn)
		if s.retry(task) {
			return
		}
		task.deliver(edgeTaskResult{err: err})
		return
	}

	select {
	case r := <-attempt:
		if r.err != nil {
			s.dropConn(conn)
			// The socket died before answering this turn, so the request was
			// never consumed upstream: replay once on a fresh connection.
			if s.retry(task) {
				return
			}
		}
		task.deliver(r)
	case <-task.ctx.Done():
		conn.forget(task.requestID)
		task.deliver(edgeTaskResult{err: task.ctx.Err()})
		// The server may still be streaming this turn; a fresh connection is
		// the only way to guarantee the next turn starts on a clean socket.
		s.dropConn(conn)
	case <-task.timer.C:
		conn.forget(task.requestID)
		task.deliver(edgeTaskResult{err: errEdgeTimeout})
		s.dropConn(conn)
	}
}

// retry re-queues a turn that never reached the server. It only ever fires
// once per task so a flapping upstream cannot loop forever.
func (s *edgeSlot) retry(task *edgeTask) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || task.retried {
		return false
	}
	if task.ctx.Err() != nil {
		return false
	}
	task.retried = true
	task.requestID = newRequestID()
	if task.timer != nil {
		task.timer.Stop()
	}
	task.timer = time.NewTimer(s.turnTimeout)
	s.queue = append([]*edgeTask{task}, s.queue...)
	return true
}

func newRequestID() string {
	id, err := randomHex(16)
	if err != nil {
		return "00000000000000000000000000000000"
	}
	return id
}

// dropConn closes conn and clears it so the next turn dials fresh.
func (s *edgeSlot) dropConn(conn *edgeConn) {
	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
	}
	s.mu.Unlock()
	conn.close()
}

// closeIdle closes the socket when the slot has been unused for at least idle.
func (s *edgeSlot) closeIdle(idle time.Duration) {
	s.mu.Lock()
	conn := s.conn
	shouldClose := conn != nil && !s.busy && len(s.queue) == 0 && time.Since(s.lastUsedAt) >= idle
	if shouldClose {
		s.conn = nil
	}
	s.mu.Unlock()
	if shouldClose {
		conn.close()
	}
}

// closeNow closes the connection immediately and fails queued tasks.
func (s *edgeSlot) closeNow() {
	s.mu.Lock()
	s.closing = true
	conn := s.conn
	s.conn = nil
	queued := s.queue
	s.queue = nil
	s.mu.Unlock()

	if conn != nil {
		conn.close()
	}
	for _, t := range queued {
		t.deliver(edgeTaskResult{err: errEdgePoolClosing})
	}
}

// queued reports the pending queue length under the slot's own lock.
func (s *edgeSlot) queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func (s *edgeSlot) stats() (alive bool, queued int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn != nil && !s.closing, len(s.queue)
}

// ---------------------------------------------------------------------------
// Pool
// ---------------------------------------------------------------------------

// edgePool maintains a fixed number of slots and dispatches incoming turns to
// the slot with the smallest queue.
type edgePool struct {
	mu      sync.Mutex
	slots   []*edgeSlot
	dialer  *websocketDialer
	cfg     edgePoolConfig
	closing bool

	initOnce sync.Once
	initErr  error

	reaperOnce sync.Once
	stop       chan struct{}
	stopOnce   sync.Once
}

type edgePoolConfig struct {
	poolSize     int
	idleTimeout  time.Duration
	turnTimeout  time.Duration
	pingInterval time.Duration
	// proxy is the parsed TTS_PROXY, reused by the voice-list HTTP client so
	// both paths tunnel through the same hop.
	proxy *proxyConfig
}

func (c edgePoolConfig) proxyURL() *url.URL {
	if c.proxy == nil {
		return nil
	}
	u := &url.URL{Scheme: "http", Host: c.proxy.host + ":" + c.proxy.port}
	if c.proxy.username != "" {
		u.User = url.UserPassword(c.proxy.username, c.proxy.password)
	}
	return u
}

func newEdgePool(dialer *websocketDialer, cfg edgePoolConfig) *edgePool {
	if cfg.poolSize < 1 {
		cfg.poolSize = 3
	}
	if cfg.idleTimeout <= 0 {
		cfg.idleTimeout = 5 * time.Minute
	}
	if cfg.turnTimeout <= 0 {
		cfg.turnTimeout = 60 * time.Second
	}
	if cfg.pingInterval <= 0 {
		cfg.pingInterval = 30 * time.Second
	}
	return &edgePool{
		dialer: dialer,
		cfg:    cfg,
		stop:   make(chan struct{}),
	}
}

// ensureInit lazily creates the slots and starts the idle reaper.
func (p *edgePool) ensureInit() error {
	p.initOnce.Do(func() {
		if p.dialer == nil {
			p.initErr = errEdgeNoDialer
			return
		}
		slots := make([]*edgeSlot, 0, p.cfg.poolSize)
		for i := 0; i < p.cfg.poolSize; i++ {
			slots = append(slots, newEdgeSlot(i, p.dialer, p.cfg))
		}
		p.mu.Lock()
		p.slots = slots
		p.mu.Unlock()
		p.reaperOnce.Do(func() { go p.reapLoop() })
	})
	return p.initErr
}

func (p *edgePool) reapLoop() {
	interval := p.cfg.idleTimeout / 4
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.mu.Lock()
			slots := append([]*edgeSlot(nil), p.slots...)
			p.mu.Unlock()
			for _, s := range slots {
				s.closeIdle(p.cfg.idleTimeout)
			}
		}
	}
}

func (p *edgePool) submit(task *edgeTask) <-chan edgeTaskResult {
	if err := p.ensureInit(); err != nil {
		task.deliver(edgeTaskResult{err: err})
		return task.result
	}

	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		task.deliver(edgeTaskResult{err: errEdgePoolClosing})
		return task.result
	}
	slots := append([]*edgeSlot(nil), p.slots...)
	p.mu.Unlock()

	// Pick the slot with the smallest queue. Queue length is guarded by each
	// slot's own mutex, so ask every slot rather than reading the field.
	best := slots[0]
	bestQueued := best.queued()
	for _, s := range slots[1:] {
		if queued := s.queued(); queued < bestQueued {
			best, bestQueued = s, queued
		}
	}
	return best.submit(task)
}

func (p *edgePool) shutdown() {
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		return
	}
	p.closing = true
	slots := p.slots
	p.slots = nil
	p.mu.Unlock()

	p.stopOnce.Do(func() { close(p.stop) })
	for _, s := range slots {
		s.closeNow()
	}
}

func (p *edgePool) stats() edgePoolStats {
	p.mu.Lock()
	slots := append([]*edgeSlot(nil), p.slots...)
	p.mu.Unlock()

	var alive, queued int
	for _, s := range slots {
		a, q := s.stats()
		if a {
			alive++
		}
		queued += q
	}
	return edgePoolStats{slots: len(slots), aliveSlots: alive, totalQueued: queued}
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var (
	errEdgeNoDialer    = &edgeError{msg: "Edge TTS pool has no dialer configured"}
	errEdgePoolClosing = &edgeError{msg: "Edge TTS pool is shutting down"}
	errEdgeConnLost    = &edgeError{msg: "Edge TTS connection lost"}
	errEdgeTimeout     = &edgeError{msg: "Edge TTS request timed out"}
)

type edgeError struct {
	msg string
}

func (e *edgeError) Error() string { return e.msg }
