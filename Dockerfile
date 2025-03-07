FROM golang:1.21-alpine as builder

WORKDIR /app

# Install dependencies required for go-translate
RUN apk add --no-cache git ca-certificates

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o translation-service .

# Create a minimal production image
FROM alpine:latest

WORKDIR /app

# Install CA certificates for making HTTPS requests to Google API
RUN apk --no-cache add ca-certificates

# Copy binary from builder stage
COPY --from=builder /app/translation-service /app/

# Expose the port
EXPOSE 8080

# Run the service
CMD ["/app/translation-service"]
