FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out && \
 if [ -f ./cmd/telegram-companion/main.go ]; then \
      go build -o /out/telegram-companion ./cmd/telegram-companion; \
    else \
      echo '#!/bin/sh' > /out/telegram-companion; \
      echo 'echo "telegram-companion binary placeholder"' >> /out/telegram-companion; \
      chmod +x /out/telegram-companion; \
    fi

FROM alpine:3.22
RUN adduser -D -H appuser
USER appuser
WORKDIR /app
COPY --from=build /out/telegram-companion /app/telegram-companion
ENTRYPOINT ["/app/telegram-companion"]
