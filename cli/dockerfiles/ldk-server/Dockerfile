FROM rust:1.85-slim-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
RUN git clone --depth=1 --branch dockerfile https://github.com/elnosh/ldk-server.git .
RUN cargo build --release --package ldk-server --package ldk-server-cli

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    gosu \
    && rm -rf /var/lib/apt/lists/*

# Create /data and the CLI default lookup dirs for tls.crt and api_key.
# The ldk-server user is created at runtime in the entrypoint so that its
# UID/GID can match the host user (same bind-mount permission trick as LND).
# Symlinks are created for both the ldk-server user and root so that
# docker exec (which runs as root by default) can also find the credentials.
RUN mkdir -p /home/ldk-server/.ldk-server/regtest \
    && mkdir -p /root/.ldk-server/regtest \
    && mkdir /data \
    && ln -s /data/tls.crt /home/ldk-server/.ldk-server/tls.crt \
    && ln -s /data/regtest/api_key /home/ldk-server/.ldk-server/regtest/api_key \
    && ln -s /data/tls.crt /root/.ldk-server/tls.crt \
    && ln -s /data/regtest/api_key /root/.ldk-server/regtest/api_key

COPY --from=builder /build/target/release/ldk-server /usr/local/bin/ldk-server
COPY --from=builder /build/target/release/ldk-server-cli /usr/local/bin/ldk-server-cli
COPY docker-entrypoint.sh /entrypoint.sh
COPY ldk-cli /usr/local/bin/ldk-cli
RUN chmod +x /entrypoint.sh /usr/local/bin/ldk-cli

ENV LDK_SERVER_NODE_REST_SERVICE_ADDRESS=0.0.0.0:3002
ENV LDK_SERVER_STORAGE_DIR_PATH=/data

EXPOSE 3001 3002

VOLUME ["/data"]

ENTRYPOINT ["/entrypoint.sh"]
CMD []
