# Upstream no longer distributes the pinned release images or archive binaries.
# Build the same source releases; these are new images, not old image replicas.
FROM --platform=$BUILDPLATFORM golang:1.25.0-alpine@sha256:f18a072054848d87a8077455f0ac8a25886f2397f88bfdd222d6fafbb5bba440 AS builder
RUN apk add --no-cache git
ENV GOTOOLCHAIN=local
WORKDIR /src

FROM builder AS minio-build
ARG TARGETARCH
RUN git init . \
    && git remote add origin https://github.com/minio/minio.git \
    && git fetch --depth=1 origin 0d7408fc9969caf07de6a8c3a84f9fbb10a6739e \
    && git checkout --detach FETCH_HEAD \
    && test "$(git rev-parse HEAD)" = 0d7408fc9969caf07de6a8c3a84f9fbb10a6739e
RUN ldflags="$(MINIO_RELEASE=RELEASE go run buildscripts/gen-ldflags.go 2025-04-22T22:12:26Z)" \
    && CGO_ENABLED=0 GOOS=linux GOARCH="$TARGETARCH" \
       go build -mod=readonly -tags kqueue -trimpath -ldflags "$ldflags" -o /out/minio .

FROM builder AS mc-build
ARG TARGETARCH
RUN git init . \
    && git remote add origin https://github.com/minio/mc.git \
    && git fetch --depth=1 origin b00526b153a31b36767991a4f5ce2cced435ee8e \
    && git checkout --detach FETCH_HEAD \
    && test "$(git rev-parse HEAD)" = b00526b153a31b36767991a4f5ce2cced435ee8e
RUN ldflags="$(MC_RELEASE=RELEASE go run buildscripts/gen-ldflags.go 2025-04-16T18:13:26Z)" \
    && CGO_ENABLED=0 GOOS=linux GOARCH="$TARGETARCH" \
       go build -mod=readonly -tags kqueue -trimpath -ldflags "$ldflags" -o /out/mc .

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce AS minio
LABEL org.opencontainers.image.source="https://github.com/minio/minio" \
      org.opencontainers.image.revision="0d7408fc9969caf07de6a8c3a84f9fbb10a6739e" \
      org.opencontainers.image.version="RELEASE.2025-04-22T22-12-26Z"
RUN apk add --no-cache ca-certificates curl
COPY --from=minio-build /out/minio /usr/local/bin/minio
COPY --from=minio-build /src/LICENSE /src/CREDITS /usr/share/licenses/minio/
ENTRYPOINT ["minio"]

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce AS mc
LABEL org.opencontainers.image.source="https://github.com/minio/mc" \
      org.opencontainers.image.revision="b00526b153a31b36767991a4f5ce2cced435ee8e" \
      org.opencontainers.image.version="RELEASE.2025-04-16T18-13-26Z"
RUN apk add --no-cache ca-certificates
COPY --from=mc-build /out/mc /usr/local/bin/mc
COPY --from=mc-build /src/LICENSE /src/CREDITS /usr/share/licenses/mc/
ENTRYPOINT ["mc"]
