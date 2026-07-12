// Package relay is the daemon-side connector to the hosted relay at repowire.io.
//
// It dials an OUTBOUND WebSocket to the relay and tunnels inbound traffic to the
// LOCAL daemon's HTTP surface (127.0.0.1), so a browser/phone hitting repowire.io
// reaches this daemon. It is a faithful port of repowire/daemon/relay_client.py
// and deliberately has ZERO coupling to the hub package: it only needs the local
// base URL and forwards to it over real HTTP (which preserves the daemon's
// localhost/auth semantics exactly — a tunneled request is a genuine local
// request, same as the Python client).
package relay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
	pingInterval   = 20 * time.Second // WS-level keepalive (Python ping_interval)
	dialTimeout    = 10 * time.Second // Python open_timeout
	tunnelTimeout  = 30 * time.Second // Python HTTP_TUNNEL_TIMEOUT
	readLimit      = 16 << 20         // 16 MiB; matches Python max_size (attachments)
)

// strippedForwardHeaders are proxy headers removed before forwarding a tunneled
// request to the local daemon, so a remote caller cannot spoof the daemon's
// require_localhost / forwarded-for checks. Mirrors relay_client.py.
var strippedForwardHeaders = map[string]bool{
	"x-forwarded-for":   true,
	"x-forwarded-proto": true,
	"x-forwarded-host":  true,
	"x-real-ip":         true,
	"forwarded":         true,
}

// Client maintains the outbound relay connection and tunnels frames.
type Client struct {
	relayURL     string // ws(s):// base (no trailing slash)
	apiKey       string
	daemonID     string
	localBaseURL string // http://127.0.0.1:<port>
	httpc        *http.Client

	mu            sync.Mutex
	running       bool
	connected     bool
	stopping      bool
	lastConnected *time.Time
	lastError     string
	lastErrorAt   *time.Time
	cancel        context.CancelFunc
	done          chan struct{}
}

// Status is the health/telemetry snapshot (mirrors relay_client.status()).
type Status struct {
	Connected     bool
	Running       bool
	URL           string
	DaemonID      string
	LastConnected *time.Time
	LastError     string
	LastErrorAt   *time.Time
}

// HealthMap projects Status onto the /health "relay" sub-object, mirroring the
// Python RelayHealth model (status ∈ connected|connecting|down).
func (s Status) HealthMap() map[string]any {
	state := "down"
	if s.Connected {
		state = "connected"
	} else if s.Running {
		state = "connecting"
	}
	m := map[string]any{
		"status":    state,
		"enabled":   true,
		"connected": s.Connected,
		"running":   s.Running,
		"url":       s.URL,
	}
	if s.LastConnected != nil {
		m["last_connected_at"] = s.LastConnected.Format(time.RFC3339Nano)
	}
	if s.LastError != "" {
		m["last_error"] = s.LastError
	}
	if s.LastErrorAt != nil {
		m["last_error_at"] = s.LastErrorAt.Format(time.RFC3339Nano)
	}
	return m
}

// NewClient builds a relay client. daemonID identifies this daemon to the relay
// (relay dedupes one connection per (user, daemon_id)); localBaseURL is the local
// daemon HTTP endpoint tunneled requests are forwarded to.
func NewClient(relayURL, apiKey, daemonID, localBaseURL string) *Client {
	return &Client{
		relayURL:     strings.TrimRight(relayURL, "/"),
		apiKey:       apiKey,
		daemonID:     daemonID,
		localBaseURL: strings.TrimRight(localBaseURL, "/"),
		httpc:        &http.Client{Timeout: tunnelTimeout},
	}
}

// Start launches the reconnect loop. Idempotent; no-op when the api key is empty
// or a loop is already running. The parent ctx bounds the loop; Stop also cancels.
func (c *Client) Start(parent context.Context) {
	if c.apiKey == "" {
		return
	}
	c.mu.Lock()
	if c.running || c.stopping {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	c.running = true
	c.done = make(chan struct{})
	c.mu.Unlock()
	go c.runLoop(ctx)
}

// EnsureRunning relaunches the loop if it died — lazy self-heal, called from
// /health (mirrors relay_client.ensure_running). Returns true if it (re)started.
func (c *Client) EnsureRunning(parent context.Context) bool {
	c.mu.Lock()
	dead := c.apiKey != "" && !c.stopping && !c.running
	c.mu.Unlock()
	if !dead {
		return false
	}
	log.Printf("relay: loop was not running; relaunching (lazy self-heal)")
	c.Start(parent)
	return true
}

// Stop signals shutdown and blocks until the loop exits.
func (c *Client) Stop() {
	c.mu.Lock()
	c.stopping = true
	cancel, done := c.cancel, c.done
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// Status returns the current telemetry snapshot.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{
		Connected:     c.connected,
		Running:       c.running,
		URL:           c.relayURL,
		DaemonID:      c.daemonID,
		LastConnected: c.lastConnected,
		LastError:     c.lastError,
		LastErrorAt:   c.lastErrorAt,
	}
}

func (c *Client) recordErr(err error) {
	now := time.Now().UTC()
	c.mu.Lock()
	c.lastError = err.Error()
	c.lastErrorAt = &now
	c.mu.Unlock()
}

func (c *Client) buildURL() string {
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	q.Set("daemon_id", c.daemonID)
	return c.relayURL + "/ws/relay?" + q.Encode()
}

func (c *Client) runLoop(ctx context.Context) {
	defer func() {
		c.mu.Lock()
		c.running = false
		c.connected = false
		close(c.done)
		c.mu.Unlock()
	}()

	backoff := initialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		connected, err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			c.recordErr(err)
			log.Printf("relay: connection lost, reconnecting in %s: %v", backoff, err)
		} else {
			log.Printf("relay: closed cleanly, reconnecting in %s", backoff)
		}
		if connected {
			backoff = initialBackoff // reset once we had a live connection
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if !connected {
			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// connectAndServe dials the relay and serves frames until the socket closes or
// ctx is cancelled. Returns whether it managed to connect (for backoff reset).
func (c *Client) connectAndServe(ctx context.Context) (bool, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	conn, _, err := websocket.Dial(dialCtx, c.buildURL(), nil)
	cancel()
	if err != nil {
		return false, err
	}
	conn.SetReadLimit(readLimit)
	defer conn.Close(websocket.StatusNormalClosure, "")

	now := time.Now().UTC()
	c.mu.Lock()
	c.connected = true
	c.lastConnected = &now
	c.mu.Unlock()
	log.Printf("relay: connected to %s", c.relayURL)
	defer func() {
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
	}()

	// WS-level keepalive: the relay does not ping the daemon, so we ping it. A
	// failed ping closes the socket, which breaks the read loop and reconnects.
	pingCtx, pingCancel := context.WithCancel(ctx)
	defer pingCancel()
	go func() {
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				pc, cancel := context.WithTimeout(pingCtx, pingInterval)
				err := conn.Ping(pc)
				cancel()
				if err != nil {
					conn.Close(websocket.StatusPolicyViolation, "ping timeout")
					return
				}
			}
		}
	}()

	// Sequential frame handling (parity with the Python `async for` listener). A
	// slow tunneled request delays the next frame but the keepalive goroutine
	// keeps the socket alive. All wsjson.Write calls stay on this goroutine, so
	// there is never a concurrent writer.
	// ponytail: sequential; if tunnel throughput ever matters, hand each frame to
	// a worker with a write mutex.
	for {
		var msg map[string]any
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			if ctx.Err() != nil {
				return true, nil
			}
			return true, err
		}
		c.handleMessage(ctx, conn, msg)
	}
}

func (c *Client) handleMessage(ctx context.Context, conn *websocket.Conn, msg map[string]any) {
	switch t, _ := msg["type"].(string); t {
	case "ping":
		_ = wsjson.Write(ctx, conn, map[string]any{"type": "pong"})
	case "http_request":
		c.handleHTTPRequest(ctx, conn, msg)
	case "relay_query", "relay_notify", "relay_broadcast":
		c.handleRelayMessage(ctx, conn, msg)
	default:
		// Unknown/opaque — ignore (Python logs at debug).
	}
}

// handleHTTPRequest tunnels a relay http_request to the local daemon and returns
// an http_response frame (base64 bodies both ways).
func (c *Client) handleHTTPRequest(ctx context.Context, conn *websocket.Conn, msg map[string]any) {
	reqID, _ := msg["request_id"].(string)
	method, _ := msg["method"].(string)
	if method == "" {
		method = "GET"
	}
	path, _ := msg["path"].(string)
	if path == "" {
		path = "/"
	}
	// HTTP MCP is deliberately local-only. A tunneled request originates from
	// the hosted relay even though the final local hop would appear loopback, so
	// reject it here before it can reach the daemon's localhost auth check.
	if path == "/mcp" || strings.HasPrefix(path, "/mcp/") {
		c.sendHTTPResponse(ctx, conn, reqID, http.StatusNotFound, nil, []byte("not found"))
		return
	}
	u := c.localBaseURL + path
	if qs, _ := msg["query_string"].(string); qs != "" {
		u += "?" + qs
	}

	var body []byte
	if b, ok := msg["body"].(string); ok && b != "" {
		body, _ = base64.StdEncoding.DecodeString(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		c.sendHTTPResponse(ctx, conn, reqID, http.StatusBadGateway, nil, []byte(err.Error()))
		return
	}
	if hs, ok := msg["headers"].(map[string]any); ok {
		for k, v := range hs {
			if strippedForwardHeaders[strings.ToLower(k)] {
				continue
			}
			if vs, ok := v.(string); ok {
				req.Header.Set(k, vs)
			}
		}
	}

	resp, err := c.httpc.Do(req)
	if err != nil {
		c.recordErr(err)
		c.sendHTTPResponse(ctx, conn, reqID, http.StatusBadGateway, nil, []byte(err.Error()))
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	headers := make(map[string]any, len(resp.Header))
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}
	c.sendHTTPResponse(ctx, conn, reqID, resp.StatusCode, headers, respBody)
}

func (c *Client) sendHTTPResponse(ctx context.Context, conn *websocket.Conn, reqID string, status int, headers map[string]any, body []byte) {
	if headers == nil {
		headers = map[string]any{}
	}
	_ = wsjson.Write(ctx, conn, map[string]any{
		"type":       "http_response",
		"request_id": reqID,
		"status":     status,
		"headers":    headers,
		"body":       base64.StdEncoding.EncodeToString(body),
	})
}

// handleRelayMessage forwards a cross-daemon relay_query/notify/broadcast to the
// matching local endpoint and returns a relay_response frame.
func (c *Client) handleRelayMessage(ctx context.Context, conn *websocket.Conn, msg map[string]any) {
	t, _ := msg["type"].(string)
	corr, _ := msg["correlation_id"].(string)
	src, _ := msg["source_daemon_id"].(string)
	endpoint := map[string]string{
		"relay_query":     "/query",
		"relay_notify":    "/notify",
		"relay_broadcast": "/broadcast",
	}[t]

	payload, _ := msg["payload"].(map[string]any)
	pb, _ := json.Marshal(payload)

	status := http.StatusBadGateway
	var respBody any = map[string]any{}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.localBaseURL+endpoint, bytes.NewReader(pb))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		resp, derr := c.httpc.Do(req)
		if derr != nil {
			c.recordErr(derr)
		} else {
			defer resp.Body.Close()
			status = resp.StatusCode
			raw, _ := io.ReadAll(resp.Body)
			if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
				var j any
				if json.Unmarshal(raw, &j) == nil {
					respBody = j
				} else {
					respBody = map[string]any{"text": string(raw)}
				}
			} else {
				respBody = map[string]any{"text": string(raw)}
			}
		}
	}
	_ = wsjson.Write(ctx, conn, map[string]any{
		"type":             "relay_response",
		"correlation_id":   corr,
		"source_daemon_id": src,
		"status":           status,
		"body":             respBody,
	})
}
