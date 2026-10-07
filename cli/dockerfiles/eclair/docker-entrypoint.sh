#!/usr/bin/env bash
set -e

# give bitcoind a second to bootup
sleep 1

# containers on linux share file permissions with hosts.
# assigning the same uid/gid from the host user
# ensures that the files can be read/write from both sides
if ! id eclair > /dev/null 2>&1; then
  USERID=${USERID:-1000}
  GROUPID=${GROUPID:-1000}

  echo "adding user eclair ($USERID:$GROUPID)"
  groupadd -f -g $GROUPID eclair
  useradd -r -u $USERID -g $GROUPID -d /home/eclair eclair
  mkdir -p /home/eclair
  chown -R $USERID:$GROUPID /home/eclair
fi

# Configuration comes from $ECLAIR_DATADIR/eclair.conf, which lightnet writes
# on the host before starting the container.
if [ "$1" = "eclair-node" ]; then
  shift
  echo "Running as eclair user:"
  echo "bash eclair-node/bin/eclair-node.sh -Declair.datadir=${ECLAIR_DATADIR} -Declair.printToConsole=true $@"
  exec gosu eclair bash eclair-node/bin/eclair-node.sh "-Declair.datadir=${ECLAIR_DATADIR}" -Declair.printToConsole=true "$@"
fi

echo "Running: $@"
exec "$@"
