# Build stage
FROM golang:1.24-alpine AS builder

# Build arguments
ARG BUILD_DATE
ARG VCS_REF
ARG VERSION

# Set working directory
WORKDIR /app

# Install git and ca-certificates for private repos
RUN apk add --no-cache git ca-certificates

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o upgrade ./main.go

# Final stage
FROM alpine:latest

# Build arguments for labels
ARG BUILD_DATE
ARG VCS_REF
ARG VERSION

# Install ca-certificates and additional tools
RUN apk add --no-cache ca-certificates git make kubectl yq jq helm

# Create non-root user
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

# Set working directory
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/main .

# Change ownership to non-root user
RUN chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Add labels
LABEL org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.title="Tools Upgrade Test" \
      org.opencontainers.image.description="A tool for testing operator upgrades" \
      org.opencontainers.image.source="https://github.com/AlaudaDevops/upgrade-test"

# Expose port (if needed)
# EXPOSE 8080

# Run the application
CMD ["./upgrade"]
