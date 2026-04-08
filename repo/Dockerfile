# ============================================================
# Stage 1 — builder
# ============================================================
FROM golang:1.22-bookworm AS builder

# CGO is required by github.com/mattn/go-sqlite3
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    libc-dev \
    && rm -rf /var/lib/apt/lists/*

ENV CGO_ENABLED=1

WORKDIR /build

# Download dependencies first (layer cache)
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN go build -tags sqlite_fts5 -o /app/server ./cmd/server

# ============================================================
# Stage 2 — runtime
# ============================================================
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libsqlite3-0 \
    wget \
    && rm -rf /var/lib/apt/lists/*

# Non-root user
RUN groupadd --gid 1001 appuser && \
    useradd --uid 1001 --gid 1001 --no-create-home --shell /usr/sbin/nologin appuser

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/server /app/server

# Copy runtime assets
COPY web/ /app/web/
COPY migrations/ /app/migrations/

# Persistent data directory owned by appuser
RUN mkdir -p /app/data && chown appuser:appuser /app/data

VOLUME ["/app/data"]

USER appuser

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:3000/health || exit 1

ENTRYPOINT ["/app/server"]
