# syntax=docker/dockerfile:1.7

# Base + deps cache
FROM golang:1.22-alpine AS deps
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
# Module download cache
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Bring in the rest of the source
COPY . .

ENV CGO_ENABLED=0


# Build API
FROM deps AS build-api
# Build cache for compiler artifacts
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# Build Worker
FROM deps AS build-worker
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker


# Runtime: API
FROM gcr.io/distroless/static-debian12 AS api
COPY --from=build-api /out/api /api
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/api"]

# Runtime: Worker
FROM gcr.io/distroless/static-debian12 AS worker
COPY --from=build-worker /out/worker /worker
USER 65532:65532
EXPOSE 9090
ENTRYPOINT ["/worker"]
