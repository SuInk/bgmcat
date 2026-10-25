FROM node:22-bookworm-slim AS ui
WORKDIR /src
COPY package.json package-lock.json ./
COPY frontend/package.json ./frontend/package.json
RUN npm ci
COPY frontend ./frontend
COPY shared ./shared
RUN npm run typecheck && npm run build
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/embed.go ./web/embed.go
COPY --from=ui /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /bgmcat ./cmd/bgmcat
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg && rm -rf /var/lib/apt/lists/* && useradd -u 10001 -m anime && mkdir /data && chown anime:anime /data
COPY --from=build /bgmcat /usr/local/bin/bgmcat
USER anime
ENV ANIME_RSS_DATA_DIR=/data ANIME_RSS_LISTEN=0.0.0.0:3001 ANIME_RSS_PUBLIC_URL=http://127.0.0.1:3001
EXPOSE 3001
VOLUME /data
ENTRYPOINT ["/usr/local/bin/bgmcat"]
