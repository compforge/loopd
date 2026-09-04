# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.0
ARG NODE_VERSION=22

FROM golang:${GO_VERSION}-bookworm AS go-builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/loop-server ./cmd/loop-server && \
    CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/router ./operators/router/cmd/router

FROM node:${NODE_VERSION}-bookworm-slim AS web-builder
WORKDIR /src/web

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM gcr.io/distroless/cc-debian12:nonroot AS loop-server
WORKDIR /var/lib/loopd
COPY --from=go-builder /out/loop-server /usr/local/bin/loop-server
ENTRYPOINT ["/usr/local/bin/loop-server"]

FROM gcr.io/distroless/cc-debian12:nonroot AS router
WORKDIR /tmp
COPY --from=go-builder /out/router /usr/local/bin/router
ENTRYPOINT ["/usr/local/bin/router"]

FROM nginxinc/nginx-unprivileged:stable-alpine AS web
COPY --from=web-builder /src/web/dist/ /usr/share/nginx/html/

