# One image per service, selected by the SVC build arg.
#
#   docker build --build-arg SVC=auth -t auth apps/services

# ─── Stage 1: build ─────────────────────────────────────────────
# go.mod pins go >= 1.25 (auto-bumped by linter) — image must match
FROM golang:1.25-alpine AS builder

ARG SVC
RUN test -n "$SVC" || (echo "ARG SVC is required (e.g. auth)" && exit 1)

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

# GET, not HEAD: gin only registers GET /healthz, and `wget --spider` sends HEAD
# and gets a 404. -O /dev/null forces a GET.
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
    CMD wget -q -O /dev/null http://localhost:${APP_PORT:-9000}/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/app"]
