# --- Build stage ------------------------------------------------------------
FROM golang:latest AS builder

WORKDIR /app

# Copy dependency files first to leverage layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy the source required by the experiment and its internal dependencies.
COPY . .

# Run the experiment's unit tests.
RUN go test ./experiments/matter-occupancy-light

# Build a static Linux binary.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" \
    -o /app/matter-occupancy-light \
    ./experiments/matter-occupancy-light

# --- Final stage ------------------------------------------------------------
FROM scratch

WORKDIR /app

COPY --from=builder /app/matter-occupancy-light /app/matter-occupancy-light

# Required for the outbound HTTPS request to api.kennedn.com.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Match the main image's non-root runtime user. The mounted Matter state must be
# readable by this UID and GID.
USER 1000:1000

ENTRYPOINT ["/app/matter-occupancy-light"]
