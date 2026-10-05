# syntax=docker/dockerfile:1.6
#
# chora-oe-grading Dockerfile — standalone Go service: the OE-grading crew's two
# ADK-Go agent binaries (oe_evaluator + oe_moderator) in one image; the
# per-deployment command selects the binary (ADR-172, single-image-two-binaries
# pattern).
#
# Build context = this repository. Shared Chora modules (chora-adk-common,
# chora-common, chora-contracts) are resolved through the Go module proxy, not
# a workspace.

ARG GO_VERSION=1.26.6
ARG ALPINE_VERSION=3.23
ARG SERVICE_NAME=chora-oe-grading
ARG GIT_SHA=unknown
ARG BUILD_TIME=unknown

############################
# Stage 1 — build
############################
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG SERVICE_NAME
ARG GIT_SHA
ARG BUILD_TIME

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY . .

RUN go mod download

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64
RUN go build -trimpath \
      -ldflags "-s -w \
        -X main.serviceName=${SERVICE_NAME} \
        -X main.gitSHA=${GIT_SHA} \
        -X main.buildTime=${BUILD_TIME}" \
      -o /out/oe_evaluator \
      ./cmd/oe_evaluator && \
    go build -trimpath \
      -ldflags "-s -w \
        -X main.serviceName=${SERVICE_NAME} \
        -X main.gitSHA=${GIT_SHA} \
        -X main.buildTime=${BUILD_TIME}" \
      -o /out/oe_moderator \
      ./cmd/oe_moderator

############################
# Stage 2 — runtime
############################
FROM gcr.io/distroless/static-debian12:nonroot

ARG SERVICE_NAME
ARG GIT_SHA
ARG BUILD_TIME

LABEL org.opencontainers.image.title="${SERVICE_NAME}" \
      org.opencontainers.image.source="https://github.com/apollo-chora/chora-oe-grading" \
      org.opencontainers.image.revision="${GIT_SHA}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.vendor="Chora Platform" \
      org.opencontainers.image.licenses="UNLICENSED" \
      io.chora.service="${SERVICE_NAME}" \
      io.chora.git-sha="${GIT_SHA}" \
      io.chora.build-time="${BUILD_TIME}"

WORKDIR /app

COPY --from=builder /out/oe_evaluator /app/oe_evaluator
COPY --from=builder /out/oe_moderator /app/oe_moderator

# Default entrypoint is the evaluator; the deployment overrides the command
# per agent: ["/app/oe_evaluator"] or ["/app/oe_moderator"], with NO
# arguments. ADR-254 D6: the binaries are subscriber-only (NATS JetStream
# dispatch in, completion out, health port only); the ADK web launcher and its
# "web -port ... agentengine" command line are gone and are refused by name if
# a stale manifest passes them.
USER 65532:65532
ENTRYPOINT ["/app/oe_evaluator"]
