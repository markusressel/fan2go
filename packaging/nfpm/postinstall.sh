#!/bin/sh
# fan2go .deb/.rpm: runs after install and upgrade.
# A new installation is not enabled or started: create /etc/fan2go/fan2go.yaml first (example:
# /usr/share/doc/fan2go/fan2go.yaml.example), then run
#   sudo systemctl enable --now fan2go
# An upgrade restarts the service if it is running (deb: "configure" with the old version, rpm: $1 >= 2).
set -e

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload > /dev/null 2>&1 || true

  upgrade=false
  case "$1" in
    configure)
      [ -z "$2" ] || upgrade=true
      ;;
    [0-9]*)
      [ "$1" -lt 2 ] || upgrade=true
      ;;
  esac
  if [ "${upgrade}" = true ]; then
    systemctl try-restart fan2go.service > /dev/null 2>&1 || true
  fi
fi

exit 0
