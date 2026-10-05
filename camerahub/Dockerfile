FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /camerahub .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 hub
COPY --from=build /camerahub /usr/local/bin/camerahub
USER hub
WORKDIR /app
ENV HUB_DATA_FILE=/app/data/sources.json
EXPOSE 8080
ENTRYPOINT ["camerahub"]
