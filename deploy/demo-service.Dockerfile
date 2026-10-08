FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/demo-service ./cmd/demo-service

FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
    && addgroup -S gopilot \
    && adduser -S -G gopilot gopilot
COPY --from=build /out/demo-service /usr/local/bin/demo-service

USER gopilot
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/demo-service"]

