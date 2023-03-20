## Build
FROM golang:1.20-alpine AS build

WORKDIR /app

COPY go.* ./

RUN go mod download

COPY *.go ./

RUN go build -o /feeder

## Deploy
FROM linuxserver/ffmpeg

WORKDIR /

COPY --from=build /feeder /feeder

ENTRYPOINT [ "/feeder" ]