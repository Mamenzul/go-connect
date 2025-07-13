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
		Name:    req.Msg.RoomName,
		Members: make(map[string]*lobbypb.PlayerInfo),
	}
	r.mu.Unlock()

	resp := &lobbypb.CreateRoomResponse{
		RoomId:  roomID,
		Success: true,
		Message: "Room created",
	}

	//make player join the room automatically
	player := &lobbypb.PlayerInfo{
		PlayerId:   req.Msg.HostPlayerId,
		PlayerName: req.Msg.PlayerName,
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
	room.Members[req.Msg.HostPlayerId] = player
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
	log.Println("Room created:", roomID, "by player:", req.Msg.HostPlayerId)

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

	delete(room.Members, req.Msg.PlayerId)
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

	if len(room.Members) == 0 {
		// If no members left, remove the room
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
