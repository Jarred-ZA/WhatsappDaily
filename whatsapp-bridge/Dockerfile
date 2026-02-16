FROM golang:1.24-bookworm AS builder

WORKDIR /build

# Install CGO dependencies for sqlite3
RUN apt-get update && apt-get install -y gcc libsqlite3-dev && rm -rf /var/lib/apt/lists/*

# Allow auto-downloading newer Go toolchain if go.mod requires it
ENV GOTOOLCHAIN=auto

# Copy go module files first for caching
COPY whatsapp-bridge/go.mod whatsapp-bridge/go.sum ./
RUN go mod download

# Copy source code
COPY whatsapp-bridge/ .

# Build with CGO enabled (required for go-sqlite3)
RUN CGO_ENABLED=1 go build -o whatsapp-bridge .

# Runtime stage
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    libsqlite3-0 \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /build/whatsapp-bridge .

EXPOSE 8080

CMD ["./whatsapp-bridge"]
