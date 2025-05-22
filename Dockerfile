FROM golang:latest AS builder

# Install build dependencies for Debian-based image
RUN apt-get update && apt-get install -y \
    git \
    ca-certificates \
    gcc \
    libc6-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application with CGO enabled for ARM64
RUN CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build -o suprnews

# Use a newer base image for runtime with ARM64 support
FROM ubuntu:22.04

# Install SQLite and dependencies
RUN apt-get update && apt-get install -y \
    sqlite3 \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/suprnews .

# Copy templates and static files
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static

# Create data directory for persistence
RUN mkdir -p /app/data && chmod 755 /app/data

# Expose the application port
EXPOSE 8080

# Run the application
CMD ["./suprnews"]
