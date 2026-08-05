# syntax=docker/dockerfile:1.7
# =============================================================================
# Sub2API Usage Viewer - Multi-Stage Dockerfile
# =============================================================================
# Stage 1: Build the Go binary (CGO_ENABLED=0 -> static, arch-neutral)
# Stage 2: Minimal alpine runtime
# =============================================================================

ARG GOLANG_IMAGE=golang:1.26.5-alpine
ARG ALPINE_IMAGE=alpine:3.21
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn

FROM --platform=${BUILDPLATFORM} ${GOLANG_IMAGE} AS builder
ARG TARGETOS
ARG TARGETARCH
ARG GOPROXY
ARG GOSUMDB
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/sub2api-usage-viewer ./cmd/viewer

FROM ${ALPINE_IMAGE}
LABEL maintainer="Wei-Shaw <github.com/Wei-Shaw>"
LABEL description="Sub2API Usage Viewer - read-only account/user/key usage reporting"
LABEL org.opencontainers.image.source="https://github.com/Wei-Shaw/sub2api"

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 1000 sub2api \
    && adduser -u 1000 -G sub2api -s /bin/sh -D sub2api \
    && mkdir -p /app/data \
    && chown sub2api:sub2api /app/data

WORKDIR /app
COPY --from=builder --chown=sub2api:sub2api /out/sub2api-usage-viewer /app/sub2api-usage-viewer

USER sub2api
EXPOSE 8081
CMD ["/app/sub2api-usage-viewer"]
