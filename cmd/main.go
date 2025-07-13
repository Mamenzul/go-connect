package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"

	"connectrpc.com/connect"
	"connectrpc.com/validate"

	lobbypbconnect "go-connect/gen/lobbypbconnect"
	"go-connect/server"
	"go-connect/utils"
)

func main() {
	srv := server.New()

	mux := http.NewServeMux()
	validateInterceptor, err := validate.NewInterceptor()
	if err != nil {
		slog.Error("error creating interceptor",
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}

	roomServicePath, roomServiceHandler := lobbypbconnect.NewRoomServiceHandler(
		srv,
		connect.WithInterceptors(validateInterceptor),
	)
	roomBroadcastPath, roomBroadcastHandler := lobbypbconnect.NewRoomBroadcastHandler(
		srv,
		connect.WithInterceptors(validateInterceptor),
	)
	mux.Handle(roomServicePath, roomServiceHandler)
	mux.Handle(roomBroadcastPath, roomBroadcastHandler)

	log.Println("✅ Connect server running on http://localhost:8080")
	if err := http.ListenAndServe(":8080", utils.WithCORS(mux)); err != nil {
		log.Fatal(err)
	}
}
