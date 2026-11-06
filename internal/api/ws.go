package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ngg-project/ngg/internal/engine"
	"github.com/ngg-project/ngg/pkg/contracts"
)

type wsHub struct {
	engine   *engine.Engine
	upgrader websocket.Upgrader
}

func newWSHub(eng *engine.Engine) *wsHub {
	return &wsHub{
		engine: eng,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				// Auth middleware already restricts this route to loopback +
				// Bearer token, so a permissive origin check is safe here.
				return true
			},
		},
	}
}

func (h *wsHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	send := make(chan []byte, 512)
	unsubscribe := h.engine.Subscribe(func(ev contracts.Event) {
		b, err := json.Marshal(ev)
		if err != nil {
			return
		}
		select {
		case send <- b:
		default:
			// Slow client: drop the event instead of blocking the whole bus.
		}
	})
	defer unsubscribe()

	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case msg, ok := <-send:
				if !ok {
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// A connection handshake is NOT an alert. v2.6.3 labelled it
	// "system.alert", which made every client pop a "系统告警" toast right
	// after login. Use a dedicated ready event; clients ignore it.
	hello, _ := json.Marshal(contracts.Event{
		Type: "system.ready", Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"message": "NGG event stream connected",
			"stage":   1,
		},
	})
	select {
	case send <- hello:
	default:
	}

	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	})
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}
