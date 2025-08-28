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
RUN export GOSUMDB=off && \
    export GOMAXPROCS=4 && \
    export GO111MODULE=on && \
    export GOFLAGS=-buildvcs=false && \
    export GOPROXY=https://build-nexus.alauda.cn/repository/golang/,direct && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o upgrade ./main.go

# Final stage
FROM alpine:latest

# Build arguments for labels
ARG BUILD_DATE
ARG VCS_REF
ARG VERSION

# Install ca-certificates and additional tools
RUN apk add --no-cache ca-certificates git make kubectl yq jq helm bash

# Create non-root user with UID 65532
RUN addgroup -g 65532 -S appgroup && \
    adduser -u 65532 -S appuser -G appgroup -h /home/appuser

# Set working directory
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/upgrade .

# Change ownership to non-root user
RUN chown -R appuser:appgroup /app && \
    chown -R appuser:appgroup /home/appuser

# Add labels
LABEL org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.title="Tools Upgrade Test" \
      org.opencontainers.image.description="A tool for testing operator upgrades" \
      org.opencontainers.image.source="https://github.com/AlaudaDevops/upgrade-test"

# Switch to non-root user
USER 65532

# Run the application
CMD ["/app/upgrade"]
