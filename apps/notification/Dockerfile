# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src

# Dependencies first so a source-only change reuses the cached layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/notification .

# distroless/static: no shell, no package manager, and runs as a non-root user
# by default — nothing to exec into if the container is ever reached.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/notification /notification
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/notification"]
