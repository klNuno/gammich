# syntax=docker/dockerfile:1.7

# The build stages run on the builder's own platform; Go cross-compiles, so
# only the runtime stage is emulated for arm64.
FROM --platform=$BUILDPLATFORM node:26-alpine AS web
WORKDIR /web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/.npmrc ./
RUN corepack install && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN rm -rf internal/staticfs/dist && mkdir -p internal/staticfs/dist
COPY --from=web /web/build/. ./internal/staticfs/dist/
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ENV CGO_ENABLED=0
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/gammich ./cmd/gammich

FROM alpine:3.24
RUN apk add --no-cache ffmpeg ca-certificates tini \
 && addgroup -S -g 1000 gammich && adduser -S -u 1000 -G gammich gammich \
 && mkdir -p /data /cache /videos \
 && chown gammich:gammich /data /cache
COPY --from=go /out/gammich /usr/local/bin/gammich
COPY LICENSE /usr/share/licenses/gammich/LICENSE
ENV GAMMICH_ADDR=:8080 \
    GAMMICH_VIDEOS=/videos \
    GAMMICH_CACHE=/cache \
    GAMMICH_DB=/data/gammich.db \
    GAMMICH_FFMPEG=/usr/bin/ffmpeg
ARG VERSION=dev
LABEL org.opencontainers.image.title="gammich" \
      org.opencontainers.image.description="Self-hosted web library for game clip recordings" \
      org.opencontainers.image.source="https://github.com/klNuno/gammich" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"
VOLUME ["/data", "/cache"]
USER gammich:gammich
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:8080/api/healthz?lite=1" || exit 1
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/gammich"]
