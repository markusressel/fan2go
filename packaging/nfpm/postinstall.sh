#!/bin/sh
# fan2go .deb/.rpm: runs after install and upgrade.
# The service is not enabled or started: configure /etc/fan2go/fan2go.yaml first, then run
#   sudo systemctl enable --now fan2go
# A running service keeps the old binary until it is restarted.
set -e

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload > /dev/null 2>&1 || true
fi

exit 0
