# syntax=docker/dockerfile:1

# ---- 1. Фронтенд (Vue + Vite) ----
FROM node:22-alpine AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ---- 2. Go: генерация gRPC-кода и сборка обоих сервисов ----
FROM golang:1.26-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest \
    && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
WORKDIR /src
COPY . .
# Свежесобранный бандл заменяет заглушку, он попадёт в бинарник через go:embed.
COPY --from=web /src/frontend/dist/ ./frontend/dist/
RUN mkdir -p internal/authpb && protoc -I proto \
      --go_out=. --go_opt=module=github.com/compico/overlaycomposer \
      --go-grpc_out=. --go-grpc_opt=module=github.com/compico/overlaycomposer \
      proto/auth/v1/auth.proto
RUN go mod tidy && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/twitch-auth ./cmd/twitch-auth && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/memealerts ./cmd/memealerts

# ---- 3. Минимальные образы ----
FROM alpine:3.22 AS base
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 app \
    && mkdir /data && chown app /data
USER app
WORKDIR /data
VOLUME /data

FROM base AS twitch-auth
COPY --from=build /out/twitch-auth /usr/local/bin/app
ENTRYPOINT ["app"]

FROM base AS memealerts
COPY --from=build /out/memealerts /usr/local/bin/app
ENTRYPOINT ["app"]
