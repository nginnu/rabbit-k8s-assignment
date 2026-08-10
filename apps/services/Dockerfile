# Unified Dockerfile — one image per service, selected by the SVC build arg.
# Build ใดใน services/cmd/* ก็ได้ด้วย ARG SVC
#
#   docker build --build-arg SVC=auth-svc -t auth-svc services/
#
# ใน docker-compose.yml:
#   build:
#     context: ./services
#     args:
#       SVC: auth-svc

# ─── Stage 1: build ─────────────────────────────────────────────
# go.mod pins go >= 1.25 (auto-bumped by linter) — image must match
FROM golang:1.25-alpine AS builder

ARG SVC
RUN test -n "$SVC" || (echo "ARG SVC is required (e.g. auth-svc)" && exit 1)

WORKDIR /src

# cache mod download
COPY go.mod go.sum* ./
RUN go mod download

# build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/app ./cmd/${SVC}

# ─── Stage 2: runtime ───────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata wget \
    && adduser -D -u 10001 app

COPY --from=builder /out/app /usr/local/bin/app

USER app
WORKDIR /home/app

# HEALTHCHECK — ใช้ GET (ไม่ใช่ HEAD) เพราะ gin register เฉพาะ GET /healthz
# `wget --spider` ใช้ HEAD ตอนแรก → 404 → ต้องใช้ -O /dev/null บังคับ GET
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
    CMD wget -q -O /dev/null http://localhost:${APP_PORT:-9000}/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/app"]
