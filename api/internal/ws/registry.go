// Package ws implements the WebSocket connection registry, keyed by user_id.
//
// Fan-out pattern:
//   - Each API instance holds a local map[userID → []conn].
//   - The wallet service publishes to Redis/Valkey channel "ws:{userID}".
//   - All instances subscribed to that channel receive and push to local connections.
//   - No sticky sessions required.
package ws

import "context"

// Conn is a minimal WebSocket connection abstraction.
type Conn interface {
	WriteMessage(messageType int, data []byte) error
}

// Registry fans out payloads to WebSocket connections keyed by user_id.
type Registry interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Register(userID, connID string, conn Conn)
	Unregister(userID, connID string)
	Broadcast(ctx context.Context, userID string, payload []byte)
}
