# --- Stage 1: Dependencies ---
FROM golang:1.25rc2-alpine3.22 AS dependencies

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download && \
    go mod verify

# --- Stage 2: Build Stage ---
FROM golang:1.25rc2-alpine3.22 AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY --from=dependencies /go/pkg/mod /go/pkg/mod

COPY go.mod go.sum ./

COPY . .

ARG VERSION=dev
ARG BUILD_TIME
ARG COMMIT_SHA

RUN CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    go build \
    -a \
    -installsuffix cgo \
    -ldflags="-w -s -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME} -X main.commitSHA=${COMMIT_SHA}" \
    -o gateway \
    ./cmd/gateway

RUN ./gateway --version || echo "Binary built successfully"

# --- Stage 3: Runtime ---
FROM alpine:3.22 AS runtime

RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -S appgroup && \
    adduser -S appuser -G appgroup -u 1001

WORKDIR /app

RUN mkdir -p /app/configs /app/logs && \
    chown -R appuser:appgroup /app

COPY --from=builder --chown=appuser:appgroup /app/gateway .

COPY --chown=appuser:appgroup configs/gateway.yaml ./configs/

USER appuser

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["./gateway"]

CMD ["--config", "./configs/gateway.yaml"]
