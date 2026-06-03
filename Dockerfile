# ---- stage 1: build ----
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache poppler-utils

WORKDIR /src
COPY go.mod ./
# No third-party dependencies; go.sum is not needed.
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /ragbot ./cmd/server

# ---- stage 2: runtime ----
FROM alpine:3.20

RUN apk add --no-cache poppler-utils ca-certificates tzdata

COPY --from=builder /ragbot /usr/local/bin/ragbot

RUN mkdir -p /data && chown nobody:nobody /data

USER nobody
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:8080/api/v1/health || exit 1

ENTRYPOINT ["ragbot", "-config", "/etc/ragbot/config.json"]
