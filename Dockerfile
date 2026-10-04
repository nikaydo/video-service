# Секреты в образ не попадают: копируется только код, а конфигурация приходит
# извне через переменные окружения.

FROM golang:1.25-alpine AS builder

WORKDIR /src

# Сначала манифесты: слой с зависимостями переиспользуется, пока не меняются
# версии. Контракт приходит из прокси модулей по версии из go.mod.
COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/video-service ./cmd

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 app

WORKDIR /app

COPY --from=builder /out/video-service /app/video-service

# Каталог хранения создаётся сервисом при старте, но он должен принадлежать
# непривилегированному пользователю: иначе запись в него не удастся.
RUN mkdir -p /app/storage/video && chown -R app:app /app/storage

# Файл .env намеренно не копируется.
USER app

EXPOSE 50053

ENTRYPOINT ["/app/video-service"]