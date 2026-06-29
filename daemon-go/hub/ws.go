package hub

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/repowire/repowire/daemon-go/peer"
	"github.com/repowire/repowire/daemon-go/proto"
)

// validNameRe / maxNameLen mirror daemon/routes/_shared.py exactly so circle and
// display_name validation matches the Python daemon byte-for-byte.
var validNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

const maxNameLen = 64

func isValidIdentifier(v string) bool {
	return v != "" && len(v) <= maxNameLen && validNameRe.MatchString(v)
}

// HandleWS is the unified /ws endpoint for every agent runtime. It mirrors the
// Python websocket_endpoint: accept, require a connect frame, authenticate,
// validate, register through the registry FSM, then run the read loop until the
// socket drops — at which point the deferred teardown severs the transport and
// cancels any in-flight queries to this peer.
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	// Lazy repair piggy-backs on a real request; never a timer.
	go h.reg.LazyRepair(context.Background())

	ctx := r.Context()
	var sessionID proto.PeerID
	registered := false

	defer func() {
		// IDENTITY-CHECKED teardown: only act if WE still own the stored socket.
		if registered {
			if removed := h.transport.Disconnect(ctx, sessionID, conn); removed {
				h.tracker.CancelQueriesToPeer(sessionID)
				_, _ = h.reg.MarkOffline(ctx, sessionID, false)
			}
		}
		_ = conn.CloseNow()
	}()

	// First frame must be connect.
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return
	}
	ftype, err := proto.ParseEnvelope(raw)
	if err != nil || ftype != proto.FrameConnect {
		_ = wsjson.Write(ctx, conn, proto.ErrorFrame{Type: proto.FrameError, Error: "First message must be connect"})
		_ = conn.Close(4000, "First message must be connect")
		return
	}

	var cf proto.ConnectFrame
	if err := json.Unmarshal(raw, &cf); err != nil {
		_ = wsjson.Write(ctx, conn, proto.ErrorFrame{Type: proto.FrameError, Error: "Malformed connect frame"})
		_ = conn.Close(4000, "Malformed connect")
		return
	}

	// Authentication: constant-time compare when a token is configured.
	if h.authToken != "" {
		if cf.AuthToken == nil || !hmac.Equal([]byte(*cf.AuthToken), []byte(h.authToken)) {
			_ = wsjson.Write(ctx, conn, proto.ErrorFrame{Type: proto.FrameError, Error: "Authentication failed"})
			_ = conn.Close(4001, "Authentication failed")
			log.Printf("ws: connection rejected: invalid or missing auth_token")
			return
		}
	}

	// Validate circle.
	circle := cf.Circle
	if circle == "" {
		circle = "default"
	}
	if !isValidIdentifier(circle) {
		_ = wsjson.Write(ctx, conn, proto.ErrorFrame{Type: proto.FrameError, Error: "Invalid circle format"})
		_ = conn.Close(4002, "Invalid circle")
		return
	}

	// Validate circle_source (None|tmux|spawn_hint|fallback).
	if cf.CircleSource != nil {
		switch *cf.CircleSource {
		case "tmux", "spawn_hint", "fallback":
		default:
			_ = wsjson.Write(ctx, conn, proto.ErrorFrame{Type: proto.FrameError, Error: "Invalid circle_source"})
			_ = conn.Close(4002, "Invalid circle_source")
			return
		}
	}

	// Validate display_name.
	if !isValidIdentifier(string(cf.DisplayName)) {
		_ = wsjson.Write(ctx, conn, proto.ErrorFrame{Type: proto.FrameError, Error: "Invalid display_name format"})
		_ = conn.Close(4002, "Invalid display_name")
		return
	}

	// Validate backend. mcp-http is a daemon-owned identity, not a ws runtime.
	backend := cf.Backend
	if backend == "" {
		backend = proto.AgentClaudeCode
	}
	if !backend.Valid() {
		_ = wsjson.Write(ctx, conn, proto.ErrorFrame{Type: proto.FrameError, Error: "Invalid backend"})
		_ = conn.Close(4002, "Invalid backend")
		return
	}
	if backend == proto.AgentMCPHTTP {
		_ = wsjson.Write(ctx, conn, proto.ErrorFrame{Type: proto.FrameError, Error: "mcp-http is not a WebSocket backend"})
		_ = conn.Close(4002, "Invalid backend")
		return
	}

	// Validate role.
	role := cf.Role
	if role == "" {
		role = proto.RoleAgent
	}
	if !role.Valid() {
		_ = wsjson.Write(ctx, conn, proto.ErrorFrame{Type: proto.FrameError, Error: "Invalid role"})
		_ = conn.Close(4002, "Invalid role")
		return
	}

	machine, _ := os.Hostname()
	params := peer.AllocateParams{
		Circle:        circle,
		Backend:       backend,
		Model:         cf.Model,
		Path:          cf.Path,
		PaneID:        cf.PaneID,
		TmuxSession:   cf.TmuxSession,
		Machine:       machine,
		Role:          role,
		ClaimedPeerID: cf.PeerID,
		AgentPID:      cf.AgentPID,
	}
	if len(cf.ModelDetails) > 0 || len(cf.Capabilities) > 0 || cf.HookVersion != nil {
		md := map[string]any{}
		if cf.HookVersion != nil {
			md["hook_version"] = *cf.HookVersion
		}
		if len(cf.Capabilities) > 0 {
			md["capabilities"] = cf.Capabilities
		}
		if len(cf.ModelDetails) > 0 {
			md["model_details"] = cf.ModelDetails
		}
		params.Metadata = md
	}

	peerID, assignedName, err := h.reg.AllocateAndRegister(ctx, params)
	if err != nil {
		if errors.Is(err, peer.ErrPeerRetired) {
			_ = wsjson.Write(ctx, conn, proto.ErrorFrame{Type: proto.FrameError, Code: "peer_retired", Error: err.Error()})
			_ = conn.Close(4004, "Peer retired")
			log.Printf("ws: connect rejected (retired peer_id)")
			return
		}
		_ = wsjson.Write(ctx, conn, proto.ErrorFrame{Type: proto.FrameError, Error: "Registration failed"})
		_ = conn.Close(4002, "Registration failed")
		return
	}
	sessionID = peerID
	registered = true

	h.transport.Connect(ctx, &ConnectionInfo{
		SessionID:   peerID,
		WS:          conn,
		PaneID:      cf.PaneID,
		DisplayName: assignedName,
		ConnectedAt: time.Now().UTC(),
	})

	if err := wsjson.Write(ctx, conn, proto.ConnectedFrame{
		Type:        proto.FrameConnected,
		SessionID:   peerID,
		DisplayName: assignedName,
	}); err != nil {
		return
	}
	log.Printf("ws: connected %s@%s (%s, %s)", assignedName, circle, peerID, backend)

	// Read loop: dispatch frames by type until the socket drops.
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}
		h.dispatch(ctx, sessionID, raw)
	}
}

// dispatch routes one inbound frame to the right handler. Unknown / malformed
// frames are logged, not fatal — a single bad message must not kill the loop.
func (h *Hub) dispatch(ctx context.Context, id proto.PeerID, raw []byte) {
	ftype, err := proto.ParseEnvelope(raw)
	if err != nil {
		log.Printf("ws: malformed frame from %s: %v", id, err)
		return
	}
	switch ftype {
	case proto.FrameResponse:
		var f proto.ResponseFrame
		if err := json.Unmarshal(raw, &f); err != nil {
			log.Printf("ws: bad response frame from %s: %v", id, err)
			return
		}
		if f.CorrelationID == "" {
			log.Printf("ws: response from %s missing correlation_id, dropping", id)
			return
		}
		h.tracker.ResolveQuery(f.CorrelationID, f.Text)

	case proto.FrameStatus:
		var f proto.StatusFrame
		if err := json.Unmarshal(raw, &f); err != nil {
			log.Printf("ws: bad status frame from %s: %v", id, err)
			return
		}
		_ = h.reg.UpdateStatus(ctx, id, normalizeStatus(f.Status))
		if f.TurnState != nil && validTurnState(*f.TurnState) {
			h.reg.UpdateTurnState(ctx, id, *f.TurnState)
		}

	case proto.FrameSetCircle:
		var f proto.SetCircleFrame
		if err := json.Unmarshal(raw, &f); err != nil {
			log.Printf("ws: bad set_circle frame from %s: %v", id, err)
			return
		}
		if f.Circle != "" && isValidIdentifier(f.Circle) {
			h.reg.SetCircle(ctx, id, f.Circle)
		} else {
			log.Printf("ws: set_circle from %s invalid circle %q", id, f.Circle)
		}

	case proto.FrameUpdateDisplayName:
		var f proto.UpdateDisplayNameFrame
		if err := json.Unmarshal(raw, &f); err != nil {
			log.Printf("ws: bad update_display_name frame from %s: %v", id, err)
			return
		}
		if isValidIdentifier(string(f.DisplayName)) {
			if _, err := h.reg.UpdateDisplayName(ctx, id, f.DisplayName); err != nil {
				log.Printf("ws: update_display_name from %s failed: %v", id, err)
			}
		} else {
			log.Printf("ws: update_display_name from %s invalid name %q", id, f.DisplayName)
		}

	case proto.FramePong:
		h.transport.ResolvePong(id, decodeMap(raw))

	case proto.FrameDeliveryAck:
		data := decodeMap(raw)
		deliveryID, _ := data["delivery_id"].(string)
		if deliveryID == "" {
			log.Printf("ws: delivery_ack from %s missing delivery_id, dropping", id)
			return
		}
		h.transport.ResolveDeliveryAck(id, deliveryID, data)

	case proto.FrameError:
		var f proto.ErrorFrame
		_ = json.Unmarshal(raw, &f)
		log.Printf("ws: client %s reported error: %s", id, f.Error)
		if f.CorrelationID != nil && *f.CorrelationID != "" {
			h.tracker.ResolveQueryError(*f.CorrelationID, errors.New(f.Error))
		}

	default:
		log.Printf("ws: unknown message type from %s: %s", id, ftype)
	}
}

// normalizeStatus mirrors the Python status_map: idle -> online; anything
// unrecognized -> online. busy/offline pass through.
func normalizeStatus(s proto.PeerStatus) proto.PeerStatus {
	switch s {
	case proto.StatusBusy:
		return proto.StatusBusy
	case proto.StatusOffline:
		return proto.StatusOffline
	case proto.StatusOnline, "idle":
		return proto.StatusOnline
	}
	return proto.StatusOnline
}

// validTurnState mirrors the Python turn_state allowlist (the empty "unknown"
// state is never accepted off the wire).
func validTurnState(ts proto.TurnState) bool {
	switch ts {
	case proto.TurnIdle, proto.TurnWorking, proto.TurnAwaitingInput, proto.TurnPendingFirstTurn:
		return true
	}
	return false
}

// decodeMap best-effort decodes a frame to a generic map for pong/delivery_ack,
// which the transport hands back to waiters verbatim.
func decodeMap(raw []byte) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}
