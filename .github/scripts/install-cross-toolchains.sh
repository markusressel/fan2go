#!/usr/bin/env bash
# Installs everything the GoReleaser build needs on an amd64 Ubuntu 24.04 (noble) machine:
# the native and cross C toolchains (cgo), binutils (readelf, for the binary checks), qemu-user-static and libsensors5
# for every target architecture (to smoke test the binaries, which load libsensors at runtime).
#
# Usage: sudo .github/scripts/install-cross-toolchains.sh
# UBUNTU_MIRROR overrides the amd64 archive mirror (default: the Azure mirror GitHub-hosted runners use),
# PORTS_MIRROR the mirror for the foreign architectures (default: ports.ubuntu.com).
set -euo pipefail

UBUNTU_MIRROR="${UBUNTU_MIRROR:-http://azure.archive.ubuntu.com/ubuntu}"
PORTS_MIRROR="${PORTS_MIRROR:-http://ports.ubuntu.com/ubuntu-ports}"
# Debian architecture names of the foreign targets (Go: arm64, ppc64le, s390x, riscv64)
FOREIGN_ARCHES=(arm64 ppc64el s390x riscv64)

for arch in "${FOREIGN_ARCHES[@]}"; do
  dpkg --add-architecture "${arch}"
done

# Pin the main archive to amd64 only. The default sources also serve foreign-arch requests to
# security.ubuntu.com, which returns 404 for them; foreign arches come from ports.ubuntu.com instead.
# Security updates come from security.ubuntu.com: multiarch packages (libc6 & co.) must have the same version for all
# architectures, and a mirror that lags behind ports.ubuntu.com makes the foreign ones uninstallable.
rm -f /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list
cat > /etc/apt/sources.list.d/ubuntu.sources << SOURCES
Types: deb
URIs: ${UBUNTU_MIRROR}
Suites: noble noble-updates noble-backports
Components: main restricted universe multiverse
Architectures: amd64
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: http://security.ubuntu.com/ubuntu
Suites: noble-security
Components: main restricted universe multiverse
Architectures: amd64
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: ${PORTS_MIRROR}
Suites: noble noble-updates noble-security
Components: main restricted universe multiverse
Architectures: ${FOREIGN_ARCHES[*]}
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
SOURCES

packages=(build-essential binutils file libsensors5 qemu-user-static)
for arch in "${FOREIGN_ARCHES[@]}"; do
  packages+=("crossbuild-essential-${arch}" "libsensors5:${arch}")
done

apt_opts=(-o Acquire::Retries=3 -o Acquire::http::Timeout=30)
apt-get "${apt_opts[@]}" update
DEBIAN_FRONTEND=noninteractive apt-get "${apt_opts[@]}" install -y --no-install-recommends "${packages[@]}"
