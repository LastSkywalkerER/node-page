########################################################
# Multi-stage Dockerfile for node-stats application
# Frontend build stage
FROM node:18-alpine AS frontend-builder

WORKDIR /app/frontend

# Copy package files
COPY frontend/package.json frontend/yarn.lock ./

# Install dependencies
RUN yarn install --frozen-lockfile

# Copy frontend source code
COPY frontend/ ./

# Build frontend
RUN yarn build

########################################################
# Backend build stage
FROM golang:1.25-alpine AS backend-builder

# Install gcc and other build dependencies for CGO
RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build backend binary
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o server ./cmd/server

########################################################
# Final runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

# Create app directory
WORKDIR /app

# Copy built frontend from frontend-builder stage
COPY --from=frontend-builder /app/dist ./dist

# Copy built backend binary from backend-builder stage
COPY --from=backend-builder /app/server .

# Expose port
EXPOSE 8080

# Run the application
CMD ["./server"]
