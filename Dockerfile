# The Dockerfile for bild whoosh image via GoReleaser.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates git openssh-client tar

WORKDIR /work
COPY whoosh /usr/bin/whoosh

ENTRYPOINT ["/usr/bin/whoosh"]
