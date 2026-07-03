FROM ghcr.io/yousysadmin/whoosh:latest AS whoosh

FROM golang:1.26.4-alpine AS builder
COPY --from=whoosh /usr/bin/whoosh /whoosh
RUN /whoosh build -o /whoosh-ext \
    --with github.com/yousysadmin/whoosh/plugins/aws \
    --with github.com/yousysadmin/whoosh/plugins/rbenv \
    --with github.com/yousysadmin/whoosh/plugins/slack

FROM alpine:3.24
COPY --from=builder /whoosh-ext /usr/bin/whoosh
RUN apk add --no-cache ca-certificates git openssh-client tar

WORKDIR /work
ENTRYPOINT ["/usr/bin/whoosh"]
