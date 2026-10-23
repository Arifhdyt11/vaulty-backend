# Satu image untuk api, worker, dan migrate (ADR-002): `vaulty <api|worker|migrate>`.
# Go di-cross-compile di platform builder (Mac arm64) supaya build amd64 tidak lewat emulasi.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/vaulty ./cmd/vaulty

# Worker butuh ffmpeg + ffprobe (Siaran: TTS → AAC) dan yt-dlp (isi video, Pro).
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata ffmpeg yt-dlp \
 && adduser -D -H -u 10001 vaulty \
 && mkdir -p /data/storage && chown -R vaulty /data
COPY --from=build /out/vaulty /usr/local/bin/vaulty
USER vaulty
ENV LOCAL_STORAGE_DIR=/data/storage
EXPOSE 8080
ENTRYPOINT ["vaulty"]
CMD ["api"]
