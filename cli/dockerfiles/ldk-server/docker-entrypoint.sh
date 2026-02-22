#!/bin/sh
set -e

# containers on linux share file permissions with hosts.
# assigning the same uid/gid from the host user
# ensures that the files can be read/write from both sides
if ! id ldk-server > /dev/null 2>&1; then
  USERID=${USERID:-1000}
  GROUPID=${GROUPID:-1000}

  echo "adding user ldk-server ($USERID:$GROUPID)"
  groupadd -f -g $GROUPID ldk-server
  useradd -r -u $USERID -g $GROUPID -d /home/ldk-server ldk-server
  chown -R $USERID:$GROUPID /home/ldk-server
fi

chown ldk-server:ldk-server /data

# ldk-server parses LDK_SERVER_BITCOIND_RPC_ADDRESS as a Rust SocketAddr,
# which requires a numeric IP — not a hostname. Resolve before exec.
if [ -n "$LDK_SERVER_BITCOIND_RPC_ADDRESS" ]; then
  _host="${LDK_SERVER_BITCOIND_RPC_ADDRESS%:*}"
  _port="${LDK_SERVER_BITCOIND_RPC_ADDRESS##*:}"
  _ip="$(getent hosts "$_host" 2>/dev/null | { read -r ip _rest; echo "$ip"; })"
  if [ -n "$_ip" ]; then
    LDK_SERVER_BITCOIND_RPC_ADDRESS="${_ip}:${_port}"
    export LDK_SERVER_BITCOIND_RPC_ADDRESS
  fi
fi

exec gosu ldk-server /usr/local/bin/ldk-server "$@"
