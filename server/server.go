package server

import (
	"context"
	"go-connect/domain/room"
	lobbypb "go-connect/gen"

	"connectrpc.com/connect"
)

type Server struct {
	RoomService room.Service
}

func New() *Server {
	return &Server{
		RoomService: room.NewInMemoryService(),
	}
}

func (s *Server) CreateRoom(ctx context.Context, req *connect.Request[lobbypb.CreateRoomRequest]) (*connect.Response[lobbypb.CreateRoomResponse], error) {
	return s.RoomService.CreateRoom(ctx, req)
}
func (s *Server) JoinRoom(ctx context.Context, req *connect.Request[lobbypb.JoinRoomRequest]) (*connect.Response[lobbypb.JoinRoomResponse], error) {
	return s.RoomService.JoinRoom(ctx, req)
}

func (s *Server) LeaveRoom(ctx context.Context, req *connect.Request[lobbypb.LeaveRoomRequest]) (*connect.Response[lobbypb.LeaveRoomResponse], error) {
	return s.RoomService.LeaveRoom(ctx, req)
}

func (s *Server) ListMembers(ctx context.Context, req *connect.Request[lobbypb.ListMembersRequest]) (*connect.Response[lobbypb.ListMembersResponse], error) {
	return s.RoomService.ListMembers(ctx, req)
}

func (s *Server) StreamRoomEvents(ctx context.Context, req *connect.Request[lobbypb.RoomEventRequest], stream *connect.ServerStream[lobbypb.RoomEvent]) error {
	return s.RoomService.StreamRoomEvents(ctx, req, stream)
}
