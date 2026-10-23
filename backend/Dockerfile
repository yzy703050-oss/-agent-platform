FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server
FROM alpine:3.23
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 platform
USER platform
COPY --from=build /out/server /usr/local/bin/server
ENV HTTP_ADDR=0.0.0.0:8080 GIN_MODE=release
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
