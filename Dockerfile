FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /syncli ./cmd/syncli

FROM alpine:3.19
RUN apk add --no-cache iproute2
COPY --from=builder /syncli /usr/local/bin/syncli
ENTRYPOINT ["syncli"]
