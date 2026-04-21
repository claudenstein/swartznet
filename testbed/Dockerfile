# Minimal container for SwartzNet testbed scenarios.
#
# The binary is statically linked (CGO_ENABLED=0) so alpine's
# musl libc is sufficient. No build step in the container — the
# binary is built on the host via scripts/build-release.sh and
# copied in as a build-arg.
#
# Usage:
#   scripts/build-release.sh testbed
#   docker build -t swartznet-testbed \
#     --build-arg BINARY=dist/swartznet-testbed-linux-amd64 .
#
# Or use docker-compose.yml which does both steps.

FROM alpine:3.20

ARG BINARY=dist/swartznet-testbed-linux-amd64

# iproute2 provides `tc` for netem network emulation.
# curl provides a lightweight probe for healthz checks.
RUN apk add --no-cache iproute2 curl

COPY ${BINARY} /usr/local/bin/swartznet
RUN chmod +x /usr/local/bin/swartznet

# Default data directory inside the container. Each container
# gets its own ephemeral filesystem; persistent state is
# mounted from the host via docker-compose volumes.
ENV SWARTZNET_DATA=/data
RUN mkdir -p /data

# Testbed fixture — a small deterministic content tree plus its
# pre-generated .torrent file. Seeds pre-populate /data with the
# content so `swartznet add /fixture/fixture.torrent` marks all
# pieces complete on startup. Leeches reference the same infohash
# via magnet+x.pe peer-hint so they can discover the seeds without
# DHT or trackers.
COPY testbed/fixture/ /fixture/

# The entrypoint script applies any netem rules, then exec's
# the daemon. Override NETEM_PROFILE to select a network
# profile (see testbed/netem/*.sh).
COPY testbed/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 42069/tcp 42069/udp 7654/tcp

ENTRYPOINT ["/entrypoint.sh"]
CMD ["swartznet", "add", "--regtest", "--data-dir=/data", "--api-addr=0.0.0.0:7654"]
