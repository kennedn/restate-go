FROM scionproto/docker-caps as caps

FROM golang:latest

# Set the working directory to /app
WORKDIR /app

# Copy your Go source code into the container
COPY . .

# Build the Go program
RUN go mod download
RUN go build -o restate main.go

# Run unit tests
RUN go test ./...

RUN chmod 775 /app/restate
COPY --from=caps /bin/setcap /bin
RUN setcap cap_net_raw=+ep /app/restate && rm /bin/setcap

ENV RESTATECONFIG=/app/config.yaml

RUN useradd -m restate
USER restate

# Set the binary as the entrypoint
ENTRYPOINT ["/app/restate"]
