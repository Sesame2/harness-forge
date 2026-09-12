FROM golang:1.25.0-alpine AS builder

WORKDIR /src/services/control-plane
COPY services/control-plane/go.mod services/control-plane/go.sum ./
RUN go mod download
COPY services/control-plane/ ./
RUN CGO_ENABLED=0 go build -o /out/harness-forge ./cmd/harness-forge \
    && CGO_ENABLED=0 go build -o /out/purge-deleted ./cmd/purge-deleted

FROM alpine:3.22

RUN apk add --no-cache curl \
    && addgroup -g 10001 app \
    && adduser -D -u 10001 -G app app
WORKDIR /app
COPY --from=builder /out/harness-forge /usr/local/bin/harness-forge
COPY --from=builder /out/purge-deleted /usr/local/bin/purge-deleted
COPY contracts/ /app/contracts/
COPY profiles/ /app/profiles/
COPY tests/fixtures/fake-runtime/ /app/fixtures/fake-runtime/
USER 10001:10001
CMD ["/usr/local/bin/harness-forge"]
