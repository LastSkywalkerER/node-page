########################################################
# Multi-stage Dockerfile for node-stats application
# Frontend build stage (dist is arch-independent — build on the native platform)
FROM --platform=$BUILDPLATFORM node:20-alpine AS frontend-builder

WORKDIR /app/frontend

# Copy package files
COPY frontend/package.json frontend/yarn.lock ./

# Install dependencies
RUN yarn install --frozen-lockfile

# Copy frontend source code
COPY frontend/ ./

# Build frontend → ../internal/webui/dist (embedded into the Go binary below)
RUN yarn build

########################################################
# Backend build stage (cross-compiles on the build platform — no QEMU Go runs)
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS backend-builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Bring in the freshly built frontend so //go:embed all:dist resolves.
# (.dockerignore excludes dist from the build context, so this is the only copy.)
COPY --from=frontend-builder /app/internal/webui/dist ./internal/webui/dist

# Build metadata + target arch (buildx provides TARGETOS/TARGETARCH).
ARG VERSION=docker
ARG COMMIT=none
ARG DATE=unknown
ARG TARGETOS=linux
ARG TARGETARCH

# Pure-Go build (modernc sqlite) — CGO off, frontend embedded. Cross-compiles
# for the target arch from the native build platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -tags embed_dist \
      -ldflags "-s -w -X system-stats/internal/version.Version=${VERSION} -X system-stats/internal/version.Commit=${COMMIT} -X system-stats/internal/version.Date=${DATE}" \
      -o server ./cmd/server

########################################################
# Final runtime stage
FROM alpine:latest

# ca-certificates for HTTPS; docker CLI + compose v2 so the `controller`
# subcommand can regenerate and apply the compose stack (Block 3).
RUN apk --no-cache add ca-certificates docker-cli docker-cli-compose

WORKDIR /app

# Copy built backend binary (frontend is embedded — no separate dist copy)
COPY --from=backend-builder /app/server .

# Expose port
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/api/v1/health || exit 1

# Run the application (default = server; `controller` selects the sidecar role)
CMD ["./server"]
