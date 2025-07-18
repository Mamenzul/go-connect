# ──────────────── 1) Build the Vite frontend with Bun ────────────────
FROM docker.io/oven/bun:1.1 as frontend-build            # lightweight Bun image

# Copy only the frontend sources (better cache utilisation)
WORKDIR /app/public/frontend
COPY public/frontend/ .                           # → /app/public/frontend

# Install deps & build
RUN bun install --frozen-lockfile                 \
 && bun run build                                 # output: /app/public/frontend/dist


# ──────────────── 2) Build the Go backend ────────────────
FROM docker.io/library/golang:1.24 as backend-build
WORKDIR /go/src/app

# Go module files first (again for cache hits)
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the backend source
COPY . .

# Bring in the built frontend
COPY --from=frontend-build /app/public/frontend/dist ./public/frontend/dist

# Build a static Go binary
RUN CGO_ENABLED=0 go build -o /go/bin/app ./cmd/


# ──────────────── 3) Minimal runtime image ────────────────
FROM gcr.io/distroless/static-debian12

# Copy binary and public assets into the final image
COPY --from=backend-build /go/bin/app /app
COPY --from=backend-build /go/src/app/public /public

CMD ["/app"]
