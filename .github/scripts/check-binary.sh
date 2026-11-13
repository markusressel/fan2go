#!/usr/bin/env bash
# Checks a fan2go binary built by GoReleaser (called as a post-build hook, see .goreleaser.yaml):
# - it doesn't need a glibc newer than MAX_GLIBC, so it runs on all distributions we support,
# - it doesn't link libsensors: fan2go loads it at runtime (libsensors.so.5 or libsensors.so.4), so one binary works
#   on all distributions,
# - the NVML build references the (lazily bound) NVML symbols, the no-nvml build doesn't reference any,
# - it starts and prints its version, and `fan2go detect` finds and loads libsensors (foreign architectures run under
#   qemu-user-static, with the libsensors5 package of the target architecture).
#
# Usage: check-binary.sh <binary> <goarch> <nvml|no-nvml>
# Without qemu or libsensors, these parts are skipped, except in CI (CI=true), where they fail.
set -euo pipefail

MAX_GLIBC="2.34"

bin="$1"
goarch="$2"
variant="$3"

fail() {
  echo "check-binary: ${bin}: $*" >&2
  exit 1
}

skip() {
  [ "${CI:-}" != "true" ] || fail "$*"
  echo "check-binary: ${bin}: $*, skipping" >&2
}

# glibc symbol versions the binary requires (from .gnu.version_r)
required_glibc="$(readelf -V -W "${bin}" | grep -oE 'GLIBC_[0-9]+(\.[0-9]+)+' | sed 's/^GLIBC_//' | sort -uV)"
[ -n "${required_glibc}" ] || fail "no GLIBC symbol versions found, is this a dynamically linked glibc binary?"
newest_glibc="$(printf '%s\n' "${required_glibc}" | tail -n 1)"
if [ "$(printf '%s\n%s\n' "${newest_glibc}" "${MAX_GLIBC}" | sort -V | tail -n 1)" != "${MAX_GLIBC}" ]; then
  fail "requires GLIBC_${newest_glibc}, newer than the supported maximum GLIBC_${MAX_GLIBC}"
fi

needed="$(readelf -d -W "${bin}" | sed -n 's/.*Shared library: \[\(.*\)\]/\1/p')"
if printf '%s\n' "${needed}" | grep -q 'libsensors'; then
  fail "links libsensors (links: ${needed//$'\n'/ }), it must be loaded at runtime"
fi
printf '%s\n' "${needed}" | grep -qxF "libc.so.6" || fail "doesn't link libc.so.6 (links: ${needed//$'\n'/ })"

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
  amd64) qemu="qemu-x86_64-static" ;;
  arm64) qemu="qemu-aarch64-static" ;;
  ppc64le) qemu="qemu-ppc64le-static" ;;
  s390x) qemu="qemu-s390x-static" ;;
  riscv64) qemu="qemu-riscv64-static" ;;
  *) fail "unknown architecture ${goarch}" ;;
esac

runner=()
if [ "${goarch}" != "$(go env GOHOSTARCH)" ]; then
  if ! command -v "${qemu}" > /dev/null; then
    skip "${qemu} not found, can't run the binary"
    echo "check-binary: ${bin}: ok (GLIBC_${newest_glibc}, ${nvml_symbols} NVML symbols, not run)"
    exit 0
  fi
  # the target's dynamic loader and libraries (libc, libsensors) come from the multiarch packages
  runner=("${qemu}")
fi

version_output="$("${runner[@]}" "${bin}" version)" || fail "'fan2go version' failed"

# detect logs the loaded libsensors at debug level; it fails if it can't run at all
detect_output="$("${runner[@]}" "${bin}" detect --verbose --no-style 2>&1)" || {
  printf '%s\n' "${detect_output}" | tail -n 20 >&2
  fail "'fan2go detect' failed"
}
libsensors="$(printf '%s\n' "${detect_output}" | grep -oE 'Detecting hwmon chips with libsensors\.so[.0-9]*' | head -n 1 ||
  true)"
if [ -z "${libsensors}" ]; then
  printf '%s\n' "${detect_output}" | grep -i 'libsensors' | head -n 3 >&2 || true
  skip "'fan2go detect' didn't load libsensors"
  libsensors="sysfs fallback"
fi

echo "check-binary: ${bin}: ok (GLIBC_${newest_glibc}, ${nvml_symbols} NVML symbols, version: ${version_output}," \
  "detect: ${libsensors#Detecting hwmon chips with })"
