FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/svaha-uni-api ./cmd/api

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S -G app app && mkdir /data && chown app:app /data
COPY --from=build /out/svaha-uni-api /usr/local/bin/svaha-uni-api
USER app
ENV DATABASE_PATH=/data/chat.db
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/svaha-uni-api"]
