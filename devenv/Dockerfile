FROM golang:1.23.0-alpine AS builder

RUN mkdir -p /app/bin

COPY . /app

WORKDIR /app/kvstore

RUN CGO_ENABLED=0 go build -o ../bin/kvstore .

RUN chmod +x /app/bin/kvstore

WORKDIR /app/devenv/http

RUN CGO_ENABLED=0 go build -o ../../bin/devsrv .

# build smaller image
FROM alpine:latest

RUN mkdir /app

COPY --from=builder /app/bin/kvstore /app
COPY --from=builder /app/bin/devsrv /app

CMD [ "/app/devsrv", "-cmd", "/app/kvstore" ]