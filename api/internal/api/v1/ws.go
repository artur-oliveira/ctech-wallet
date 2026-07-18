package v1

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"gopkg.aoctech.app/api/internal/middleware"
	"gopkg.aoctech.app/api/internal/ws"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
)

const wsPingInterval = 30 * time.Second

const wsAuthTimeout = 5 * time.Second

// readAuthToken reads the first WebSocket frame after the upgrade and extracts
// the bearer JWT. The client sends it as {"token":"..."} (or a raw token) once;
// a missing or unreadable frame fails closed so no connection hangs open.
func readAuthToken(conn *fws.Conn) (string, bool) {
	_ = conn.SetReadDeadline(time.Now().Add(wsAuthTimeout))
	defer conn.SetReadDeadline(time.Time{})
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return "", false
	}
	return extractBearer(msg), true
}

// extractBearer returns the token from a {"token":"..."} JSON frame, falling
// back to the raw trimmed frame body when it isn't JSON.
func extractBearer(msg []byte) string {
	var p struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(msg, &p) == nil && p.Token != "" {
		return p.Token
	}
	return strings.TrimSpace(string(msg))
}

// wsAllowedOrigin mirrors the HTTP CORS policy for the WebSocket upgrade:
// when no origins are configured (dev) every origin is allowed; otherwise only
// listed origins may connect. A missing Origin header (non-browser clients) is
// always allowed. This blocks cross-site WebSocket hijacking (CSWSH) without
// diverging from the CORS config the rest of the app uses.
func wsAllowedOrigin(ctx *fasthttp.RequestCtx, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	origin := string(ctx.Request.Header.Peek("Origin"))
	if origin == "" {
		return true
	}
	for _, a := range allowed {
		if a == origin {
			return true
		}
	}
	return false
}

// RegisterWS registers the GET /ws WebSocket upgrade endpoint. Auth is a query
// param (?token=<jwt>) rather than a header — the browser WebSocket API cannot
// set Authorization on the upgrade request.
//
// SECURITY (M3): the JWT is no longer taken from the ?token= query string (it
// leaked into ALB/CloudFront access logs and browser history). The client now
// sends it as the first post-upgrade text frame — see readAuthToken below. A
// short read deadline fails closed if that frame never arrives.
func RegisterWS(router fiber.Router, verifier *middleware.Verifier, reg ws.Registry, allowedOrigins []string) {
	upgrader := fws.FastHTTPUpgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(ctx *fasthttp.RequestCtx) bool { return wsAllowedOrigin(ctx, allowedOrigins) },
	}
	router.Get("/ws", func(c fiber.Ctx) error {
		return upgrader.Upgrade(c.RequestCtx(), func(conn *fws.Conn) {
			ctx := c.Context()
			send := func(msg any) {
				data, _ := json.Marshal(msg)
				_ = conn.WriteMessage(fws.TextMessage, data)
			}

			// M3: auth moved off the ?token= query string (it leaked into LB/CF
			// access logs and browser history). The client sends the JWT as the
			// first text frame immediately after the upgrade; we read exactly one
			// frame under a short deadline, then clear it so the steady-state read
			// loop blocks until the next ping.
			token, ok := readAuthToken(conn)
			if !ok {
				send(map[string]any{"type": "error", "code": "unauthorized", "message": "Token ausente ou inválido"})
				_ = conn.Close()
				return
			}

			claims, err := verifier.VerifyClaims(ctx, token)
			if err != nil || claims == nil || claims.Sub == "" {
				send(map[string]any{"type": "error", "code": "unauthorized", "message": "Token inválido ou expirado"})
				_ = conn.Close()
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
