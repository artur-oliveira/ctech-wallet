package v1

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/artur-oliveira/ctech-wallet/api/internal/middleware"
	"github.com/artur-oliveira/ctech-wallet/api/internal/ws"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

const wsPingInterval = 30 * time.Second

var wsUpgrader = fws.FastHTTPUpgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(_ *fasthttp.RequestCtx) bool { return true },
}

// RegisterWS registers the GET /ws WebSocket upgrade endpoint. Auth is a query
// param (?token=<jwt>) rather than a header — the browser WebSocket API cannot
// set Authorization on the upgrade request.
func RegisterWS(router fiber.Router, verifier *middleware.Verifier, reg ws.Registry) {
	router.Get("/ws", func(c fiber.Ctx) error {
		token := c.Query("token")
		if token == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "token obrigatório"})
		}

		return wsUpgrader.Upgrade(c.RequestCtx(), func(conn *fws.Conn) {
			ctx := c.Context()
			send := func(msg any) {
				data, _ := json.Marshal(msg)
				_ = conn.WriteMessage(fws.TextMessage, data)
			}

			claims, err := verifier.VerifyClaims(ctx, token)
			if err != nil || claims == nil || claims.Sub == "" {
				send(map[string]any{"type": "error", "code": "unauthorized", "message": "Token inválido ou expirado"})
				return
			}
			userID := claims.Sub

			connID := uuid.NewString()
			reg.Register(userID, connID, &wsConnAdapter{conn: conn})
			defer reg.Unregister(userID, connID)

			send(map[string]any{"type": "connected", "conn_id": connID})
			slog.Info("ws connected", "conn", connID, "user", userID)

			done := make(chan struct{})
			go func() {
				t := time.NewTicker(wsPingInterval)
				defer t.Stop()
				for {
					select {
					case <-t.C:
						if e := conn.WriteMessage(fws.TextMessage, []byte(`{"type":"ping"}`)); e != nil {
							return
						}
					case <-done:
						return
					}
				}
			}()

			for {
				if _, _, e := conn.ReadMessage(); e != nil {
					break
				}
			}
			close(done)
			slog.Info("ws disconnected", "conn", connID, "user", userID)
		})
	})
}

// wsConnAdapter adapts fasthttp/websocket.Conn to ws.Conn.
type wsConnAdapter struct {
	conn *fws.Conn
}

func (w *wsConnAdapter) WriteMessage(messageType int, data []byte) error {
	return w.conn.WriteMessage(messageType, data)
}
