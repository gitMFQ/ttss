package tts

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// websocketDialer dials the Edge TTS WebSocket. It honours TTS_PROXY,
// HTTPS_PROXY and HTTP_PROXY environment variables (TTS_PROXY wins). Only
// HTTP CONNECT proxies are supported — this covers mihomo/Clash/V2RayN
// default ports.
type websocketDialer struct {
	proxy        *proxyConfig
	handshakeTO  time.Duration
	maxFrameSize int64

	// dialEdgeOverride is a test seam: when set, dialEdge returns its result
	// instead of opening a real connection.
	dialEdgeOverride func() (*websocket.Conn, error)
}

type proxyConfig struct {
	raw      string
	host     string
	port     string
	username string
	password string
}

// pickProxyEnv returns the first non-empty value among TTS_PROXY,
// HTTPS_PROXY and HTTP_PROXY. Whitespace is trimmed.
func pickProxyEnv() string {
	for _, key := range []string{"TTS_PROXY", "HTTPS_PROXY", "HTTP_PROXY"} {
		if v := strings.TrimSpace(osGetenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func newWebsocketDialer() *websocketDialer {
	raw := pickProxyEnv()
	d := &websocketDialer{
		handshakeTO:  15 * time.Second,
		maxFrameSize: 32 << 20,
	}
	if raw != "" {
		cfg, err := parseProxyURL(raw)
		if err != nil {
			fmt.Printf("[EdgeTTS] ignoring proxy %q: %v\n", raw, err)
		} else {
			d.proxy = cfg
			fmt.Printf("[EdgeTTS] using proxy %s\n", cfg.host+":"+cfg.port)
		}
	}
	if d.proxy == nil {
		fmt.Printf("[EdgeTTS] direct (no proxy)\n")
	}
	return d
}

func parseProxyURL(raw string) (*proxyConfig, error) {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return nil, fmt.Errorf("proxy URL must start with http:// or https://")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse proxy URL: %w", err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "8080"
	}
	if host == "" {
		return nil, fmt.Errorf("proxy URL missing host")
	}
	cfg := &proxyConfig{raw: raw, host: host, port: port}
	if u.User != nil {
		cfg.username = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			cfg.password = pw
		}
	}
	return cfg, nil
}

func (d *websocketDialer) dialEdge() (*websocket.Conn, error) {
	if d.dialEdgeOverride != nil {
		return d.dialEdgeOverride()
	}
	connectionID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	secMSGEC, err := generateSecMSGECv2(float64(time.Now().Unix()))
	if err != nil {
		return nil, fmt.Errorf("generate Sec-MS-GEC token: %w", err)
	}
	const targetHost = "speech.platform.bing.com"
	const targetPort = "443"

	if d.proxy != nil {
		return d.dialViaProxy(connectionID, secMSGEC, targetHost, targetPort)
	}
	return d.dialDirect(connectionID, secMSGEC)
}

func (d *websocketDialer) dialDirect(connectionID, secMSGEC string) (*websocket.Conn, error) {
	u := buildEdgeWSURL(connectionID, secMSGEC)
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = d.handshakeTO
	header := http.Header{}
	header.Set("Origin", "chrome-extension://jdiccldimpdaibmpdkjnbmckianbfold")
	header.Set("User-Agent", edgeUserAgent)
	conn, resp, err := dialer.Dial(u.String(), header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("connect Edge TTS: %w (HTTP %d)", err, resp.StatusCode)
		}
		return nil, fmt.Errorf("connect Edge TTS: %w", err)
	}
	conn.SetReadLimit(d.maxFrameSize)
	return conn, nil
}

// dialViaProxy opens a TCP connection to the proxy, sends HTTP CONNECT,
// performs TLS over the tunneled socket, then runs the WebSocket upgrade.
// We implement this by hand to avoid pulling in golang.org/x/net/proxy.
func (d *websocketDialer) dialViaProxy(connectionID, secMSGEC, targetHost, targetPort string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d.handshakeTO)
	defer cancel()

	rawConn, err := (&net.Dialer{Timeout: d.handshakeTO}).DialContext(ctx, "tcp", net.JoinHostPort(d.proxy.host, d.proxy.port))
	if err != nil {
		return nil, fmt.Errorf("connect proxy %s:%s: %w", d.proxy.host, d.proxy.port, err)
	}

	connectReq := fmt.Sprintf("CONNECT %s:%s HTTP/1.1\r\nHost: %s:%s\r\n", targetHost, targetPort, targetHost, targetPort)
	if d.proxy.username != "" {
		connectReq += "Proxy-Authorization: Basic " + basicAuth(d.proxy.username, d.proxy.password) + "\r\n"
	}
	connectReq += "\r\n"
	if _, err := rawConn.Write([]byte(connectReq)); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("send CONNECT to proxy: %w", err)
	}

	br := bufio.NewReader(rawConn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("read proxy status: %w", err)
	}
	if !strings.Contains(statusLine, "200") {
		rawConn.Close()
		return nil, fmt.Errorf("proxy CONNECT rejected: %s", strings.TrimSpace(statusLine))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("read proxy headers: %w", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	tlsConn := tls.Client(&prefixReader{r: br, conn: rawConn}, &tls.Config{
		ServerName: targetHost,
		MinVersion: tls.VersionTLS12,
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("TLS handshake via proxy: %w", err)
	}

	u := buildEdgeWSURL(connectionID, secMSGEC)
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = d.handshakeTO
	// NetDialTLSContext tells gorilla the TLS handshake is already done, so it
	// will not try to run a second one over our tunnel.
	dialer.NetDialTLSContext = func(context.Context, string, string) (net.Conn, error) { return tlsConn, nil }
	header := http.Header{}
	header.Set("Origin", "chrome-extension://jdiccldimpdaibmpdkjnbmckianbfold")
	header.Set("User-Agent", edgeUserAgent)
	conn, resp, err := dialer.Dial(u.String(), header)
	if err != nil {
		tlsConn.Close()
		if resp != nil {
			return nil, fmt.Errorf("WebSocket upgrade via proxy: %w (HTTP %d)", err, resp.StatusCode)
		}
		return nil, fmt.Errorf("WebSocket upgrade via proxy: %w", err)
	}
	conn.SetReadLimit(d.maxFrameSize)
	return conn, nil
}

// prefixReader lets us pass a bufio.Reader that's already buffered some of
// the proxy response into a net.Conn that tls.Client can drive.
type prefixReader struct {
	r    *bufio.Reader
	conn net.Conn
}

func (p *prefixReader) Read(b []byte) (int, error)         { return p.r.Read(b) }
func (p *prefixReader) Write(b []byte) (int, error)        { return p.conn.Write(b) }
func (p *prefixReader) Close() error                       { return p.conn.Close() }
func (p *prefixReader) LocalAddr() net.Addr                { return p.conn.LocalAddr() }
func (p *prefixReader) RemoteAddr() net.Addr               { return p.conn.RemoteAddr() }
func (p *prefixReader) SetDeadline(t time.Time) error      { return p.conn.SetDeadline(t) }
func (p *prefixReader) SetReadDeadline(t time.Time) error  { return p.conn.SetReadDeadline(t) }
func (p *prefixReader) SetWriteDeadline(t time.Time) error { return p.conn.SetWriteDeadline(t) }

func basicAuth(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

func buildEdgeWSURL(connectionID, secMSGEC string) url.URL {
	u := url.URL{
		Scheme: "wss",
		Host:   "speech.platform.bing.com",
		Path:   "/consumer/speech/synthesize/readaloud/edge/v1",
	}
	q := u.Query()
	q.Set("TrustedClientToken", trustedClientToken)
	q.Set("Sec-MS-GEC", secMSGEC)
	q.Set("Sec-MS-GEC-Version", secMSGECVersion)
	q.Set("ConnectionId", connectionID)
	u.RawQuery = q.Encode()
	return u
}

// ---------------------------------------------------------------------------
// edgeConn wraps a websocket.Conn and multiplexes turns by request id.
//
// Ownership: pending maps request id -> the connection's own pendingTurn. The
// connection is the single writer of every pendingTurn, always under c.mu, so
// callers never share mutable state with the read loop.
// ---------------------------------------------------------------------------

const (
	edgeReadTimeout  = 70 * time.Second
	edgeWriteTimeout = 15 * time.Second
)

// pendingTurn accumulates one attempt. The channel is buffered so the read loop
// can always deliver without blocking, even if the waiter has moved on.
type pendingTurn struct {
	audio  []byte
	result chan edgeTaskResult
}

func newPendingTurn() *pendingTurn {
	return &pendingTurn{result: make(chan edgeTaskResult, 1)}
}

// finish delivers the attempt outcome; it is called at most once per turn.
func (p *pendingTurn) finish(r edgeTaskResult) {
	select {
	case p.result <- r:
	default:
	}
}

type edgeConn struct {
	ws           *websocket.Conn
	pingInterval time.Duration

	mu      sync.Mutex
	pending map[string]*pendingTurn
	closed  bool

	done      chan struct{}
	closeOnce sync.Once
}

func newEdgeConn(ws *websocket.Conn, pingInterval time.Duration) *edgeConn {
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	c := &edgeConn{
		ws:           ws,
		pingInterval: pingInterval,
		pending:      make(map[string]*pendingTurn),
		done:         make(chan struct{}),
	}
	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(edgeReadTimeout))
		return nil
	})
	go c.readLoop()
	go c.pingLoop()
	return c
}

// sendTurn writes the speech.config + SSML frames for the task and returns the
// channel that will carry this attempt's result.
func (c *edgeConn) sendTurn(task *edgeTask) (<-chan edgeTaskResult, error) {
	turn := newPendingTurn()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errEdgeConnLost
	}
	c.pending[task.requestID] = turn
	c.mu.Unlock()

	config := map[string]any{"context": map[string]any{"synthesis": map[string]any{"audio": map[string]any{
		"metadataoptions": map[string]string{"sentenceBoundaryEnabled": "false", "wordBoundaryEnabled": "false"},
		"outputFormat":    EdgeOutputFormat,
	}}}}
	configJSON, _ := json.Marshal(config)
	configFrame := fmt.Sprintf("X-Timestamp:%s\r\nContent-Type:application/json; charset=utf-8\r\nPath:speech.config\r\n\r\n%s", edgeTimestamp(), configJSON)
	if err := c.writeText(configFrame); err != nil {
		c.forget(task.requestID)
		return nil, fmt.Errorf("send Edge speech configuration: %w", err)
	}
	ssml := buildEdgeSSML(task.text, task.options)
	ssmlFrame := fmt.Sprintf("X-Timestamp:%s\r\nX-RequestId:%s\r\nContent-Type:application/ssml+xml\r\nPath:ssml\r\n\r\n%s", edgeTimestamp(), task.requestID, ssml)
	if err := c.writeText(ssmlFrame); err != nil {
		c.forget(task.requestID)
		return nil, fmt.Errorf("send Edge SSML: %w", err)
	}
	return turn.result, nil
}

func (c *edgeConn) writeText(frame string) error {
	_ = c.ws.SetWriteDeadline(time.Now().Add(edgeWriteTimeout))
	return c.ws.WriteMessage(websocket.TextMessage, []byte(frame))
}

// forget removes a pending turn without completing it. Used when the caller
// timed out or was cancelled and the connection is being dropped.
func (c *edgeConn) forget(requestID string) {
	c.mu.Lock()
	delete(c.pending, requestID)
	c.mu.Unlock()
}

// isClosed reports whether the connection has already been torn down.
func (c *edgeConn) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// fail tears the connection down exactly once and completes every pending turn
// with err. It never blocks: each channel is buffered.
func (c *edgeConn) fail(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		pending := c.pending
		c.pending = make(map[string]*pendingTurn)
		c.mu.Unlock()

		close(c.done)
		_ = c.ws.Close()
		for _, turn := range pending {
			turn.finish(edgeTaskResult{err: err})
		}
	})
}

func (c *edgeConn) close() {
	c.fail(errEdgeConnLost)
}

func (c *edgeConn) pingLoop() {
	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(edgeWriteTimeout)); err != nil {
				c.fail(fmt.Errorf("Edge TTS ping failed: %w", err))
				return
			}
		}
	}
}

func (c *edgeConn) readLoop() {
	for {
		_ = c.ws.SetReadDeadline(time.Now().Add(edgeReadTimeout))
		kind, message, err := c.ws.ReadMessage()
		if err != nil {
			c.fail(err)
			return
		}
		switch kind {
		case websocket.BinaryMessage:
			c.handleAudioFrame(message)
		case websocket.TextMessage:
			c.handleTextFrame(message)
		case websocket.CloseMessage:
			c.fail(errEdgeConnLost)
			return
		}
	}
}

func (c *edgeConn) handleAudioFrame(message []byte) {
	if len(message) < 2 {
		return
	}
	headerLength := int(binary.BigEndian.Uint16(message[:2]))
	if headerLength < 1 || len(message) < headerLength+2 {
		return
	}
	headers := parseEdgeHeaders(string(message[2 : 2+headerLength]))
	rid := headers["x-requestid"]
	if rid == "" {
		return
	}
	payload := message[2+headerLength:]
	if len(payload) == 0 {
		return
	}
	c.mu.Lock()
	turn, ok := c.pending[rid]
	if ok {
		turn.audio = append(turn.audio, payload...)
	}
	c.mu.Unlock()
}

func (c *edgeConn) handleTextFrame(message []byte) {
	raw := string(message)
	headerEnd := strings.Index(raw, "\r\n\r\n")
	if headerEnd < 0 {
		return
	}
	headers := parseEdgeHeaders(raw[:headerEnd])
	if !strings.EqualFold(headers["path"], "turn.end") {
		return
	}
	rid := headers["x-requestid"]
	if rid == "" {
		return
	}
	c.mu.Lock()
	turn, ok := c.pending[rid]
	if ok {
		delete(c.pending, rid)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	if len(turn.audio) == 0 {
		turn.finish(edgeTaskResult{err: errors.New("Edge TTS returned empty audio")})
		return
	}
	turn.finish(edgeTaskResult{audio: turn.audio})
}
