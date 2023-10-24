# Use the UBI9 as the base image
FROM golang:latest

# Set the working directory to /app
WORKDIR /app

# Copy your Go source code into the container
COPY . .

# Build the Go program
RUN go mod download
RUN go build -o restate main.go

# Create a non-root user to run the application
RUN useradd -r -u 999 restate

# Set permissions to the user for the application directory
RUN chown -R restate:restate .

# Switch to the non-root user
USER restate

ENV RESTATECONFIG=/app/config.yaml

# Set the binary as the entrypoint
ENTRYPOINT ["/app/restate"]
