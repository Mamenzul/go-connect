package room

import (
	"context"
	"errors"
	lobbypb "go-connect/gen"
	"log"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

type Service interface {
	CreateRoom(ctx context.Context, req *connect.Request[lobbypb.CreateRoomRequest]) (*connect.Response[lobbypb.CreateRoomResponse], error)
	JoinRoom(ctx context.Context, req *connect.Request[lobbypb.JoinRoomRequest]) (*connect.Response[lobbypb.JoinRoomResponse], error)
	LeaveRoom(ctx context.Context, req *connect.Request[lobbypb.LeaveRoomRequest]) (*connect.Response[lobbypb.LeaveRoomResponse], error)
	ListMembers(ctx context.Context, req *connect.Request[lobbypb.ListMembersRequest]) (*connect.Response[lobbypb.ListMembersResponse], error)
	StreamRoomEvents(ctx context.Context, req *connect.Request[lobbypb.RoomEventRequest], stream *connect.ServerStream[lobbypb.RoomEvent]) error
	SendMessage(ctx context.Context, req *connect.Request[lobbypb.PlayerSentMessageRequest]) (*connect.Response[lobbypb.PlayerSentMessageResponse], error)
	Ping(ctx context.Context, req *connect.Request[lobbypb.PingRequest]) (*connect.Response[lobbypb.PingResponse], error)
}

type InMemoryService struct {
	mu    *sync.RWMutex
	rooms map[string]*Room
}

func NewInMemoryService() Service {
	return &InMemoryService{
		mu:    &sync.RWMutex{},
		rooms: make(map[string]*Room),
	}
}

func (r InMemoryService) CreateRoom(ctx context.Context, req *connect.Request[lobbypb.CreateRoomRequest]) (*connect.Response[lobbypb.CreateRoomResponse], error) {
	roomID := uuid.New().String()

	r.mu.Lock()
	r.rooms[roomID] = &Room{
		Members: make(map[string]*lobbypb.PlayerInfo),
	}
	r.mu.Unlock()

	resp := &lobbypb.CreateRoomResponse{
		RoomId:  roomID,
		Success: true,
		Message: "Room created",
	}

	//make player join the room automatically as the host
	player := &lobbypb.PlayerInfo{
		PlayerId:   req.Msg.PlayerId,
		PlayerName: req.Msg.PlayerName,
		IsHost:     true,
	}

	r.mu.Lock()
	room, ok := r.rooms[roomID]
	if !ok {
		r.mu.Unlock()
		return connect.NewResponse(&lobbypb.CreateRoomResponse{
			Success: false,
			Message: "Failed to create room",
		}), nil
	}
	room.Members[req.Msg.PlayerId] = player
	r.mu.Unlock()
	// Broadcast room created event
	if room.broadcast != nil {
		event := &lobbypb.RoomEvent{

			Event: &lobbypb.RoomEvent_PlayerJoined{
				PlayerJoined: &lobbypb.PlayerJoinedBroadcast{
					RoomId:    roomID,
					Player:    player,
					Timestamp: time.Now().Unix(),
				},
			},
		}
		for _, ch := range room.broadcast {
			select {
			case ch <- event:
			default:
			}
		}
	}
	log.Println("Room created:", roomID, "by player:", req.Msg.PlayerId)

	return connect.NewResponse(resp), nil
}

func (r InMemoryService) JoinRoom(ctx context.Context, req *connect.Request[lobbypb.JoinRoomRequest]) (*connect.Response[lobbypb.JoinRoomResponse], error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	room, ok := r.rooms[req.Msg.RoomId]
	if !ok {
		return connect.NewResponse(&lobbypb.JoinRoomResponse{
			Success: false,
			Message: "Room not found",
		}), nil
	}

	player := &lobbypb.PlayerInfo{
		PlayerId:   req.Msg.PlayerId,
		PlayerName: req.Msg.PlayerName,
		IsHost:     false,
	}
	room.Members[req.Msg.PlayerId] = player

	// Broadcast player joined event
	if room.broadcast != nil {
		event := &lobbypb.RoomEvent{
			Event: &lobbypb.RoomEvent_PlayerJoined{
				PlayerJoined: &lobbypb.PlayerJoinedBroadcast{
					RoomId:    req.Msg.RoomId,
					Player:    player,
					Timestamp: time.Now().Unix(),
				},
			},
		}
		for _, ch := range room.broadcast {
			select {
			case ch <- event:
			default:
			}
		}
	}
	log.Println("Player", req.Msg.PlayerId, "joined room:", req.Msg.RoomId)
	return connect.NewResponse(&lobbypb.JoinRoomResponse{
		Success: true,
		Message: "Joined room",
	}), nil
}

// RoomBroadcast implementation
func (r InMemoryService) StreamRoomEvents(
	ctx context.Context,
	req *connect.Request[lobbypb.RoomEventRequest],
	stream *connect.ServerStream[lobbypb.RoomEvent],
) error {
	r.mu.Lock()
	room, ok := r.rooms[req.Msg.RoomId]
	if !ok {
		r.mu.Unlock()
		return connect.NewError(connect.CodeNotFound, errors.New("room not found"))
	}
	if room.broadcast == nil {
		room.broadcast = make(map[int]chan *lobbypb.RoomEvent)
	}
	room.bcID++
	id := room.bcID
	ch := make(chan *lobbypb.RoomEvent, 10)
	room.broadcast[id] = ch
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(room.broadcast, id)
		r.mu.Unlock()
		close(ch)
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case evt := <-ch:
			if err := stream.Send(evt); err != nil {
				return err
			}
		}
	}
}

func (r InMemoryService) LeaveRoom(ctx context.Context, req *connect.Request[lobbypb.LeaveRoomRequest]) (*connect.Response[lobbypb.LeaveRoomResponse], error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	room, ok := r.rooms[req.Msg.RoomId]
	if !ok {
		return connect.NewResponse(&lobbypb.LeaveRoomResponse{
			Success: false,
			Message: "Room not found",
		}), nil
	}

	player, exists := room.Members[req.Msg.PlayerId]
	if !exists {
		return connect.NewResponse(&lobbypb.LeaveRoomResponse{
			Success: false,
			Message: "Player not found in room",
		}), nil
	}

	// If the player is the host, transfer host to another player before leaving in a safe manner
	if player.IsHost {
		for _, p := range room.Members {
			if p.PlayerId != req.Msg.PlayerId {
				p.IsHost = true // Transfer host to the first found player
				log.Println("Host transferred from", req.Msg.PlayerId, "to", p.PlayerId, "in room:", req.Msg.RoomId)
				break
			}
		}
	}

	// Remove the player from the room
	delete(room.Members, req.Msg.PlayerId)

	// Broadcast that the player has left
	if room.broadcast != nil {
		event := &lobbypb.RoomEvent{
			Event: &lobbypb.RoomEvent_PlayerLeft{
				PlayerLeft: &lobbypb.PlayerLeftBroadcast{
					RoomId:    req.Msg.RoomId,
					Player:    player,
					Timestamp: time.Now().Unix(),
				},
			},
		}
		for _, ch := range room.broadcast {
			select {
			case ch <- event:
			default:
			}
		}
	}

	log.Println("Player", req.Msg.PlayerId, "left room:", req.Msg.RoomId)

	// If the room is now empty, delete it
	if len(room.Members) == 0 {
		delete(r.rooms, req.Msg.RoomId)
		log.Println("Room", req.Msg.RoomId, "deleted as it has no members")
	}

	return connect.NewResponse(&lobbypb.LeaveRoomResponse{
		Success: true,
		Message: "Left room",
	}), nil
}

func (r InMemoryService) ListMembers(ctx context.Context, req *connect.Request[lobbypb.ListMembersRequest]) (*connect.Response[lobbypb.ListMembersResponse], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	room, ok := r.rooms[req.Msg.RoomId]
	if !ok {
		return connect.NewResponse(&lobbypb.ListMembersResponse{}), nil
	}

	var players []*lobbypb.PlayerInfo
	for _, p := range room.Members {
		players = append(players, p)
	}

	return connect.NewResponse(&lobbypb.ListMembersResponse{
		Players: players,
	}), nil
}

func (r InMemoryService) SendMessage(ctx context.Context, req *connect.Request[lobbypb.PlayerSentMessageRequest]) (*connect.Response[lobbypb.PlayerSentMessageResponse], error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	room, ok := r.rooms[req.Msg.RoomId]
	if !ok {
		return connect.NewResponse(&lobbypb.PlayerSentMessageResponse{
			Success: false,
			Message: "Room not found",
		}), nil
	}

	_, exists := room.Members[req.Msg.PlayerId]
	if !exists {
		return connect.NewResponse(&lobbypb.PlayerSentMessageResponse{
			Success: false,
			Message: "Player not found in room",
		}), nil
	}

	// find player in map by id
	player, exists := room.Members[req.Msg.PlayerId]
	if !exists {
		return connect.NewResponse(&lobbypb.PlayerSentMessageResponse{
			Success: false,
			Message: "Player not found in room",
		}), nil
	}
	if room.broadcast != nil {
		event := &lobbypb.RoomEvent{
			Event: &lobbypb.RoomEvent_ChatMessageBroadcast{
				ChatMessageBroadcast: &lobbypb.ChatMessageBroadcast{
					RoomId:    req.Msg.RoomId,
					Player:    player,
					Message:   req.Msg.Message,
					Timestamp: time.Now().Unix(),
				},
			},
		}
		for _, ch := range room.broadcast {
			select {
			case ch <- event:
			default:
				log.Println("Failed to send message to broadcast channel, channel full or closed")
			}
		}
	}

	log.Println("Player", req.Msg.PlayerId, "sent message in room:", req.Msg.RoomId)

	return connect.NewResponse(&lobbypb.PlayerSentMessageResponse{
		Success: true,
		Message: "Message sent",
	}), nil
}

func (r InMemoryService) Ping(
	ctx context.Context,
	req *connect.Request[lobbypb.PingRequest],
) (*connect.Response[lobbypb.PingResponse], error) {
	r.mu.RLock()
	room, ok := r.rooms[req.Msg.RoomId]
	r.mu.RUnlock()

	if !ok {
		return connect.NewResponse(&lobbypb.PingResponse{
			ServerTimeUnixMillis: time.Now().UnixMilli(),
		}), nil
	}

	now := time.Now().UnixMilli()
	latency := now - req.Msg.ClientTimeUnixMillis

	// Broadcast PingBroadcast as RoomEvent
	if room.broadcast != nil {
		event := &lobbypb.RoomEvent{
			Event: &lobbypb.RoomEvent_PingBroadcast{
				PingBroadcast: &lobbypb.PingBroadcast{
					RoomId:               req.Msg.RoomId,
					PlayerId:             req.Msg.PlayerId,
					LatencyMs:            latency,
					ServerTimeUnixMillis: now,
				},
			},
		}
		for _, ch := range room.broadcast {
			select {
			case ch <- event:
			default:
				// Avoid blocking on full channels
			}
		}
	}

	// Also return server time as response (optional)
	resp := &lobbypb.PingResponse{
		ServerTimeUnixMillis: now,
	}

	return connect.NewResponse(resp), nil
}
