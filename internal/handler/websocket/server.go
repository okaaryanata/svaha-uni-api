package websocket

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	domain "github.com/okaaryanata/svaha-uni-api/internal/domain/chat"
)

type peer struct {
	id     domain.Identity
	conn   *websocket.Conn
	queue  chan any
	cancel context.CancelFunc
}
type rateEntry struct {
	start time.Time
	count int
}
type Server struct {
	connections      int
	store            ChatService
	mu               sync.Mutex
	peers            map[*peer]bool
	origins          map[string]bool
	rates            map[string]rateEntry
	closed           bool
	heartbeatEvery   time.Duration
	heartbeatTimeout time.Duration
}

func NewServer(store ChatService, origins []string) *Server {
	allowed := map[string]bool{}
	for _, origin := range origins {
		allowed[strings.TrimSpace(origin)] = true
	}
	return &Server{store: store, peers: map[*peer]bool{}, origins: allowed, rates: map[string]rateEntry{}, heartbeatEvery: 20 * time.Second, heartbeatTimeout: 5 * time.Second}
}
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/chat/demos", s.create)
	mux.HandleFunc("POST /api/v1/chat/sessions", s.login)
	mux.HandleFunc("GET /api/v1/chat/ws", s.socket)
}
func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("write JSON response", "error", err)
	}
}
func httpError(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]any{"error": map[string]string{"message": message}})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		httpError(w, 415, "use application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		httpError(w, 400, "invalid JSON")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		httpError(w, 400, "send one JSON object")
		return false
	}
	return true
}
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !decode(w, r, &body) {
		return
	}
	s.mu.Lock()
	now := time.Now()
	for ip, entry := range s.rates {
		if now.Sub(entry.start) > time.Minute {
			delete(s.rates, ip)
		}
	}
	// RemoteAddr is deliberately not taken from an untrusted forwarded header.
	ip := r.RemoteAddr
	if cut := strings.LastIndex(ip, ":"); cut >= 0 {
		ip = ip[:cut]
	}
	entry := s.rates[ip]
	if entry.count >= 10 || len(s.rates) >= 2000 {
		s.mu.Unlock()
		httpError(w, 429, "too many demo requests; retry in a minute")
		return
	}
	if entry.start.IsZero() {
		entry.start = now
	}
	entry.count++
	s.rates[ip] = entry
	s.mu.Unlock()
	if err := s.store.Prune(r.Context()); err != nil {
		httpError(w, 503, "unable to clean up demos; retry later")
		return
	}
	room, secret, err := s.store.Create(r.Context())
	if err != nil {
		httpError(w, 503, "unable to create demo; retry later")
		return
	}
	jsonResponse(w, 201, map[string]any{"room_id": room, "recovery_token": secret, "accounts": []map[string]string{{"id": "a", "name": "Demo A"}, {"id": "b", "name": "Demo B"}}, "retention_hours": 24})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Room   string `json:"room_id"`
		Secret string `json:"recovery_token"`
		Actor  string `json:"account_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	token, err := s.store.Login(r.Context(), body.Room, body.Secret, body.Actor)
	if err != nil {
		httpError(w, 401, "demo expired or recovery credential is invalid")
		return
	}
	jsonResponse(w, 200, map[string]string{"token": token, "account_id": body.Actor})
}
func (s *Server) socket(w http.ResponseWriter, r *http.Request) {
	if !s.origins[r.Header.Get("Origin")] {
		httpError(w, 403, "WebSocket origin is not allowed")
		return
	}
	s.mu.Lock()
	if s.closed || s.connections >= 100 {
		s.mu.Unlock()
		httpError(w, 503, "chat connection limit reached")
		return
	}
	s.connections++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.connections--; s.mu.Unlock() }()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(16384)
	authCtx, authCancel := context.WithTimeout(r.Context(), 5*time.Second)
	_, data, err := conn.Read(authCtx)
	authCancel()
	if err != nil {
		return
	}
	var auth struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &auth) != nil || auth.Type != "auth" {
		_ = conn.Close(websocket.StatusPolicyViolation, "authentication required")
		return
	}
	id, err := s.store.Authenticate(r.Context(), auth.Token)
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "session expired")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	p := &peer{id: id, conn: conn, queue: make(chan any, 16), cancel: cancel}
	// Subscription and initial snapshot share the same serialization lock as writes.
	s.mu.Lock()
	if s.closed || len(s.peers) >= 100 {
		s.mu.Unlock()
		return
	}
	s.peers[p] = true
	initial, err := s.store.Snapshot(ctx, id)
	if err != nil {
		delete(s.peers, p)
		s.mu.Unlock()
		return
	}
	p.queue <- initial
	s.presenceLocked(id.Room)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.peers, p)
		s.typingLocked(id, false)
		s.presenceLocked(id.Room)
		s.mu.Unlock()
	}()
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-p.queue:
				payload, err := json.Marshal(event)
				if err != nil {
					return
				}
				writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				err = conn.Write(writeCtx, websocket.MessageText, payload)
				stop()
				if err != nil {
					return
				}
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(s.heartbeatEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.store.Authenticate(ctx, auth.Token); err != nil {
					cancel()
					return
				}
				pingCtx, stop := context.WithTimeout(ctx, s.heartbeatTimeout)
				err := conn.Ping(pingCtx)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	window := time.Now()
	commands := 0
	for {
		_, data, err = conn.Read(ctx)
		if err != nil {
			return
		}
		if time.Since(window) > time.Second {
			window = time.Now()
			commands = 0
		}
		commands++
		if commands > 40 {
			_ = conn.Close(websocket.StatusPolicyViolation, "too many commands")
			return
		}
		var command domain.Command
		if json.Unmarshal(data, &command) != nil || command.Type != "command" {
			s.enqueue(p, map[string]any{"type": "error", "message": "invalid command"})
			continue
		}
		if command.Action == "typing" {
			s.mu.Lock()
			s.typingLocked(id, command.Enabled)
			s.mu.Unlock()
			continue
		}
		if _, err := s.store.Authenticate(ctx, auth.Token); err != nil {
			_ = conn.Close(websocket.StatusPolicyViolation, "session expired")
			return
		}
		s.mu.Lock()
		result, err := s.store.Execute(ctx, id, command)
		if err == nil {
			for other := range s.peers {
				if other.id.Room != id.Room || (result.ErrorCode != "" && other != p) {
					continue
				}
				snapshot, e := s.store.Snapshot(ctx, other.id)
				if e == nil {
					s.enqueueLocked(other, snapshot)
				}
			}
			if result.ErrorCode == "" {
				s.enqueueLocked(p, map[string]any{"type": "ack", "id": command.ID, "message_id": result.MessageID, "revision": result.Revision})
			} else {
				s.enqueueLocked(p, map[string]any{"type": "error", "id": command.ID, "code": result.ErrorCode, "message": result.ErrorMessage, "revision": result.Revision, "retryable": false})
			}
		} else {
			slog.Warn("chat command failed", "error", err)
			s.enqueueLocked(p, map[string]any{"type": "error", "id": command.ID, "code": "temporarily_unavailable", "message": "Unable to save; this operation will retry.", "retryable": true})
		}

		s.mu.Unlock()
	}
}
func (s *Server) enqueue(p *peer, event any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueueLocked(p, event)
}
func (s *Server) enqueueLocked(p *peer, event any) {
	select {
	case p.queue <- event:
	default:
		p.cancel()
	}
}

// Presence is room-scoped and aggregates all authenticated sockets for an account.
func (s *Server) presenceLocked(room string) {
	accounts := map[string]bool{"a": false, "b": false}
	for p := range s.peers {
		if p.id.Room == room {
			accounts[p.id.Actor] = true
		}
	}
	event := struct {
		Type     string          `json:"type"`
		Accounts map[string]bool `json:"accounts"`
	}{Type: "presence", Accounts: accounts}
	for p := range s.peers {
		if p.id.Room == room {
			s.enqueueLocked(p, event)
		}
	}
}
func (s *Server) typingLocked(id domain.Identity, enabled bool) {
	for p := range s.peers {
		if p.id.Room == id.Room && p.id.Actor != id.Actor {
			s.enqueueLocked(p, map[string]any{"type": "typing", "account_id": id.Actor, "enabled": enabled})
		}
	}
}
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for p := range s.peers {
		p.cancel()
	}
}

// ChatService is the use-case boundary consumed by HTTP and WebSocket transports.
type ChatService interface {
	Create(context.Context) (string, string, error)
	Login(context.Context, string, string, string) (string, error)
	Authenticate(context.Context, string) (domain.Identity, error)
	Prune(context.Context) error
	Snapshot(context.Context, domain.Identity) (domain.Snapshot, error)
	Execute(context.Context, domain.Identity, domain.Command) (domain.CommandResult, error)
}
