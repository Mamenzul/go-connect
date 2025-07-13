package room

import (
	lobbypb "go-connect/gen"
)

// In-memory room model
type Room struct {
	Name      string
	Members   map[string]*lobbypb.PlayerInfo
	broadcast map[int]chan *lobbypb.RoomEvent // Add broadcast channels per room
	bcID      int                             // Incremental ID for broadcast channels
}
