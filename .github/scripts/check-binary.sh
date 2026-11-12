#!/usr/bin/env bash
# Checks a fan2go binary built by GoReleaser (called as a post-build hook, see .goreleaser.yaml):
# - it doesn't need a glibc newer than MAX_GLIBC, so it runs on all distributions we support,
# - it links the expected libsensors soname (libsensors.so.5, or libsensors.so.4 for the .rpm build),
# - the NVML build references the (lazily bound) NVML symbols, the no-nvml build doesn't reference any,
# - it starts and prints its version (foreign architectures run under qemu-user-static).
#
# Usage: check-binary.sh <binary> <goarch> <nvml|no-nvml> [libsensors soname, default: libsensors.so.5]
# Without qemu, the smoke test of foreign binaries is skipped, except in CI (CI=true), where it fails.
set -euo pipefail

MAX_GLIBC="2.34"

bin="$1"
goarch="$2"
variant="$3"
libsensors="${4:-libsensors.so.5}"

fail() {
  echo "check-binary: ${bin}: $*" >&2
  exit 1
}

# glibc symbol versions the binary requires (from .gnu.version_r)
required_glibc="$(readelf -V -W "${bin}" | grep -oE 'GLIBC_[0-9]+(\.[0-9]+)+' | sed 's/^GLIBC_//' | sort -uV)"
[ -n "${required_glibc}" ] || fail "no GLIBC symbol versions found, is this a dynamically linked glibc binary?"
newest_glibc="$(printf '%s\n' "${required_glibc}" | tail -n 1)"
if [ "$(printf '%s\n%s\n' "${newest_glibc}" "${MAX_GLIBC}" | sort -V | tail -n 1)" != "${MAX_GLIBC}" ]; then
  fail "requires GLIBC_${newest_glibc}, newer than the supported maximum GLIBC_${MAX_GLIBC}"
fi

needed="$(readelf -d -W "${bin}" | sed -n 's/.*Shared library: \[\(.*\)\]/\1/p')"
printf '%s\n' "${needed}" | grep -qxF "${libsensors}" || fail "doesn't link ${libsensors} (links: ${needed//$'\n'/ })"

nvml_symbols="$(readelf --dyn-syms -W "${bin}" | grep -c 'nvml' || true)"
case "${variant}" in
  nvml)
    [ "${nvml_symbols}" -ge 3 ] || fail "NVML build has only ${nvml_symbols} NVML symbols, expected NVML support"
    ;;
  no-nvml)
    [ "${nvml_symbols}" -eq 0 ] || fail "no-nvml build references ${nvml_symbols} NVML symbols"
    ;;
  *)
    fail "unknown variant '${variant}'"
    ;;
esac

case "${goarch}" in
  amd64) triplet="x86_64-linux-gnu" qemu="qemu-x86_64-static" ;;
  arm64) triplet="aarch64-linux-gnu" qemu="qemu-aarch64-static" ;;
  ppc64le) triplet="powerpc64le-linux-gnu" qemu="qemu-ppc64le-static" ;;
  s390x) triplet="s390x-linux-gnu" qemu="qemu-s390x-static" ;;
  riscv64) triplet="riscv64-linux-gnu" qemu="qemu-riscv64-static" ;;
  *) fail "unknown architecture ${goarch}" ;;
esac

# The build machine (Debian/Ubuntu) only has libsensors.so.5. For a binary that links another soname (the .rpm
# build), point that soname at it, so the smoke test also checks that the patched binary loads.
lib_dir=""
if [ "${libsensors}" != "libsensors.so.5" ]; then
  lib_dir="$(mktemp -d)"
  trap 'rm -rf "${lib_dir}"' EXIT
  host_lib="/usr/lib/${triplet}/libsensors.so.5"
  if [ "${goarch}" = "$(go env GOHOSTARCH)" ]; then
    host_lib="$(ldconfig -p | awk '$1 == "libsensors.so.5" { print $NF; exit }')"
  fi
  [ -e "${host_lib}" ] || fail "libsensors.so.5 for ${goarch} not found on this machine"
  ln -s "${host_lib}" "${lib_dir}/${libsensors}"
fi

runner=()
if [ "${goarch}" != "$(go env GOHOSTARCH)" ]; then
  if ! command -v "${qemu}" > /dev/null; then
    [ "${CI:-}" != "true" ] || fail "${qemu} not found"
    echo "check-binary: ${bin}: ${qemu} not found, skipping the smoke test" >&2
    echo "check-binary: ${bin}: ok (GLIBC_${newest_glibc}, ${nvml_symbols} NVML symbols, not run)"
    exit 0
  fi
  # the target's dynamic loader and libraries (libc, libsensors) come from the multiarch packages
  runner=("${qemu}")
fi

version_output="$(LD_LIBRARY_PATH="${lib_dir}" "${runner[@]}" "${bin}" version)" || fail "'fan2go version' failed"
echo "check-binary: ${bin}: ok (GLIBC_${newest_glibc}, ${nvml_symbols} NVML symbols, version: ${version_output})"
