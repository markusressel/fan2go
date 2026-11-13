#!/bin/sh
# fan2go .deb/.rpm: runs after removal and after an upgrade.
set -e

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload > /dev/null 2>&1 || true
fi

exit 0
