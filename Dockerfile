FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 vaulty
WORKDIR /app
COPY --from=build /out/ /app/
USER vaulty
EXPOSE 8080
# docker-compose memilih binary lewat command: /app/api, /app/worker, atau /app/migrate
CMD ["/app/api"]
