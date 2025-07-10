# --- Build Stage ---
FROM golang:1.25rc2-alpine3.22 AS builder
RUN apk update && apk upgrade --no-cache

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o gateway ./cmd/gateway

FROM alpine:3.20
RUN apk update && apk upgrade --no-cache

# RUN addgroup -S appgroup && adduser -S appuser -G appgroup
# USER appuser

WORKDIR /app
COPY --from=builder /app/gateway .
COPY configs/gateway.yaml ./configs/gateway.yaml
EXPOSE 8080
ENTRYPOINT ["./gateway"]

# CMD ["--config", "./configs/gateway.yaml"] # Eğer gateway binary'si argüman alıyorsa
