# Dockerfile собирает сервис из локальной копии контракта.
#
# По умолчанию подтягивается опубликованная версия. Для локальной сборки
# положите контракт рядом и передайте флаг:
#
#   docker build --build-arg CONTRACT_PATH=../grpc-contract .

FROM golang:1.24-alpine AS builder

ARG CONTRACT_PATH=""

WORKDIR /src

# Сначала манифесты: слой с зависимостями переиспользуется, пока не меняются
# версии.
COPY go.mod go.sum ./

# Контракт нужен до go mod download, потому что он объявлен в require.
COPY ${CONTRACT_PATH} /src/grpc-contract

RUN if [ -d /src/grpc-contract/proto ]; then \
      go mod edit -replace github.com/nikaydo/grpc-contract=/src/grpc-contract; \
    fi \
 && go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/video-service ./cmd

FROM alpine:3.20

# ca-certificates нужен, если сервис станет обращаться к внешним API.
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 app

WORKDIR /app

COPY --from=builder /out/video-service /app/video-service
COPY --from=builder /src/db /app/db

# Конфигурация приходит извне: файл .env в образ не копируется намеренно,
# иначе секреты попали бы в слои.
USER app

EXPOSE 50053

ENTRYPOINT ["/app/video-service"]