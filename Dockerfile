# Satu image untuk api, worker, dan migrate (ADR-002): `vaulty <api|worker|migrate>`.
# Go di-cross-compile di platform builder (server amd64, atau Mac arm64 untuk BUILD=local) tanpa emulasi.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/vaulty ./cmd/vaulty

# yt-dlp dari rilis resmi, bukan paket Alpine: paket distro tertinggal berbulan-bulan, sementara
# extractor TikTok/Instagram/YouTube sering berubah. Binary musllinux sudah membawa curl_cffi
# (impersonasi browser) yang mengurangi penolakan TikTok "Unable to extract webpage video data".
# SHA2-256SUMS di-ADD dulu supaya cache layer pecah tiap ada rilis baru: tiap deploy membawa versi terbaru.
FROM alpine:3.22 AS ytdlp
ARG TARGETARCH
ADD https://github.com/yt-dlp/yt-dlp/releases/latest/download/SHA2-256SUMS /tmp/SHA2-256SUMS
RUN set -eu; \
    case "$TARGETARCH" in amd64) asset=yt-dlp_musllinux ;; arm64) asset=yt-dlp_musllinux_aarch64 ;; *) echo "arsitektur $TARGETARCH tidak didukung" >&2; exit 1 ;; esac; \
    wget -qO "/tmp/$asset" "https://github.com/yt-dlp/yt-dlp/releases/latest/download/$asset"; \
    cd /tmp && grep " $asset\$" SHA2-256SUMS | sha256sum -c -; \
    install -m 755 "/tmp/$asset" /out-yt-dlp

# Worker butuh ffmpeg + ffprobe (Siaran: TTS → AAC), yt-dlp (isi video, Pro), dan deno
# (runtime JS yang dipakai yt-dlp untuk tantangan YouTube).
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata ffmpeg deno \
 && adduser -D -H -u 10001 vaulty \
 && mkdir -p /data/storage && chown -R vaulty /data
COPY --from=ytdlp /out-yt-dlp /usr/local/bin/yt-dlp
COPY --from=build /out/vaulty /usr/local/bin/vaulty
USER vaulty
ENV LOCAL_STORAGE_DIR=/data/storage
EXPOSE 8080
ENTRYPOINT ["vaulty"]
CMD ["api"]
