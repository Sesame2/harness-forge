FROM golang:1.25.0-alpine AS builder

WORKDIR /src/services/control-plane
COPY services/control-plane/go.mod services/control-plane/go.sum ./
RUN go mod download
COPY services/control-plane/ ./
RUN CGO_ENABLED=0 go build ./cmd/harness-forge

FROM alpine:3.22

RUN apk add --no-cache curl
WORKDIR /app
COPY --from=builder /src/services/control-plane/harness-forge /usr/local/bin/harness-forge
COPY contracts/ /app/contracts/
COPY profiles/ /app/profiles/
CMD ["/usr/local/bin/harness-forge"]
