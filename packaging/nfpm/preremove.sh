#!/bin/sh
# fan2go .deb/.rpm: runs before removal and before an upgrade replaces the package.
# On removal (deb: "remove", rpm: 0 remaining instances), stop and disable the service (fan2go applies
# its configured exit behaviour to the fans when it stops). Upgrades leave it running; postinstall restarts it.
set -e

case "$1" in
  remove | 0)
    if [ -d /run/systemd/system ]; then
      systemctl --no-reload disable --now fan2go.service > /dev/null 2>&1 || true
    fi
    ;;
esac

exit 0
