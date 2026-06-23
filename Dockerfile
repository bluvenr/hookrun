# Build stage
FROM golang:1.23-alpine AS builder

ARG VERSION=dev
ARG BUILD_TIME

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X github.com/bluvenr/hookrun/internal/version.Version=${VERSION} \
              -X 'github.com/bluvenr/hookrun/internal/version.BuildTime=${BUILD_TIME}' \
              -s -w" \
    -trimpath \
    -o hookrun ./cmd/hookrun

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -S hookrun && adduser -S hookrun -G hookrun

WORKDIR /app

COPY --from=builder /build/hookrun /usr/local/bin/hookrun

# Default directories (users mount their own config.yaml and hooks/)
RUN mkdir -p /app/hooks && chown -R hookrun:hookrun /app

USER hookrun

EXPOSE 9000

ENTRYPOINT ["hookrun"]
CMD ["start", "-f"]
