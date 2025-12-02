# --- Build stage ------------------------------------------------------------
FROM golang:latest AS builder

WORKDIR /app

# Copy dependency files first to leverage layer caching
COPY go.mod go.sum ./
RUN go mod download

# Now copy the rest of the source
COPY . .

# Run unit tests
RUN go test ./...

# Build a static Linux binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /app/restate main.go

# --- Final stage ------------------------------------------------------------
FROM scratch

WORKDIR /app

# Copy the compiled binary from the builder stage
COPY --from=builder /app/restate /app/restate

# Default config path – overridden/populated by a ConfigMap volume in Kubernetes
ENV RESTATECONFIG=/app/config.yaml

# Run as non-root
USER 1000:1000

ENTRYPOINT ["/app/restate"]
