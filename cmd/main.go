package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

	// gRPC Connect handlers
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

	// Static file and index.html fallback handler
	distDir := "./public/frontend/dist"
	fs := http.FileServer(http.Dir(distDir))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// If the request matches a known gRPC route, let those handlers handle it
		if strings.HasPrefix(r.URL.Path, roomServicePath) || strings.HasPrefix(r.URL.Path, roomBroadcastPath) {
			mux.ServeHTTP(w, r)
			return
		}

		// Attempt to serve static asset
		path := filepath.Join(distDir, r.URL.Path)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}

		// Fallback to index.html for client-side routing
		http.ServeFile(w, r, filepath.Join(distDir, "index.html"))
	})

	log.Println("✅ Server running on http://localhost:9090")
	if err := http.ListenAndServe(":9090", utils.WithCORS(mux)); err != nil {
		log.Fatal(err)
	}
}
