#!/usr/bin/env bash
# Checks the .deb and .rpm packages GoReleaser built in <dist-dir>:
# - the package version is the release version, and for a pre-release (e.g. 1.0.0-rc.1, or a snapshot) it sorts
#   below the final version (1.0.0) for dpkg and rpm, so installing the final release upgrades a pre-release,
# - the file names have no characters that GitHub replaces in release asset names (e.g. "~"); a renamed asset would
#   no longer match its line in SHA256SUMS,
# - SHA256SUMS lists every package.
#
# Usage: .github/scripts/check-packages.sh <dist-dir>
# Needs dpkg-deb, dpkg and rpm (on Ubuntu: apt-get install rpm).
set -euo pipefail

dist="${1:?usage: $0 <dist-dir>}"
fail() {
  echo "check-packages: $*" >&2
  exit 1
}

for tool in dpkg dpkg-deb rpm; do
  command -v "${tool}" >/dev/null || fail "${tool} is missing"
done

version="$(sed -n 's/.*"version":"\([^"]*\)".*/\1/p' "${dist}/metadata.json")"
[ -n "${version}" ] || fail "no version in ${dist}/metadata.json"
# 1.0.0-rc.1+abc -> final 1.0.0
final="${version%%[-+]*}"
prerelease=false
case "${version}" in
  "${final}"-*) prerelease=true ;;
esac

# rpmvercmp of two versions: -1, 0 or 1
rpm_vercmp() {
  rpm --eval "%{lua: print(rpm.vercmp('$1', '$2'))}"
}

shopt -s nullglob
debs=("${dist}"/*.deb)
rpms=("${dist}"/*.rpm)
[ "${#debs[@]}" -gt 0 ] || fail "no .deb packages in ${dist}"
[ "${#rpms[@]}" -gt 0 ] || fail "no .rpm packages in ${dist}"

for pkg in "${debs[@]}" "${rpms[@]}"; do
  name="$(basename "${pkg}")"
  [[ "${name}" =~ ^[A-Za-z0-9._+-]+$ ]] || fail "${name}: the file name has characters GitHub renames"
  grep -q "  ${name}\$" "${dist}/SHA256SUMS" || fail "${name} is missing in SHA256SUMS"

  case "${pkg}" in
    *.deb)
      pkg_version="$(dpkg-deb -f "${pkg}" Version)"
      if ${prerelease}; then
        dpkg --compare-versions "${pkg_version}" lt "${final}" ||
          fail "${name}: version ${pkg_version} doesn't sort below ${final} (dpkg)"
      else
        [ "${pkg_version}" = "${final}" ] || fail "${name}: version ${pkg_version}, expected ${final}"
      fi
      ;;
    *.rpm)
      pkg_version="$(rpm -qp --nosignature --qf '%{VERSION}-%{RELEASE}' "${pkg}" 2>/dev/null)"
      if ${prerelease}; then
        [ "$(rpm_vercmp "${pkg_version}" "${final}-1")" = "-1" ] ||
          fail "${name}: version ${pkg_version} doesn't sort below ${final}-1 (rpm)"
      else
        [ "${pkg_version}" = "${final}-1" ] || fail "${name}: version ${pkg_version}, expected ${final}-1"
      fi
      ;;
  esac
  echo "check-packages: ${name}: ok (${pkg_version})"
done
