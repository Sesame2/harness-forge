FROM golang:1.25.0-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build ./cmd/harness-forge

FROM alpine:3.22

RUN apk add --no-cache curl
COPY --from=builder /src/harness-forge /usr/local/bin/harness-forge
CMD ["/usr/local/bin/harness-forge"]
