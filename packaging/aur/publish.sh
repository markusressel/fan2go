#!/usr/bin/env bash
#
# Publishes the fan2go and fan2go-git AUR packages for an existing release tag.
#
# Runs locally on Arch Linux with the maintainer's AUR SSH key, which deliberately never leaves the maintainer's
# machine (no CI secret). See RELEASING.md.
#
# Usage: packaging/aur/publish.sh [--yes] [--pkgrel N] [--skip-build] <version>
#
#   <version>       release tag to publish, e.g. 0.15.1 (must already be pushed to GitHub)
#   --pkgrel N      pkgrel for the stable package (default: 1); use for packaging-only fixes of the same version
#   --skip-build    only generate .SRCINFO, don't test-build the packages
#   --yes           push without asking for confirmation
#
# Environment:
#   AUR_DIR         directory containing the AUR clones fan2go/ and fan2go-git/ (default: ~/programming);
#                   missing clones are created (fan2go-git is expected at $AUR_DIR/fan2go-git)

set -euo pipefail

usage() {
	sed -n '/^# Usage:/,/^$/s/^# \{0,1\}//p' "$0"
	exit "${1:-1}"
}

die() {
	echo "error: $*" >&2
	exit 1
}

info() {
	echo "==> $*"
}

assume_yes=false
pkgrel=1
skip_build=false
version=""

while [ $# -gt 0 ]; do
	case "$1" in
	--yes) assume_yes=true ;;
	--pkgrel)
		[ $# -ge 2 ] || die "--pkgrel needs a value"
		pkgrel="$2"
		shift
		;;
	--skip-build) skip_build=true ;;
	-h | --help) usage 0 ;;
	-*) die "unknown option: $1" ;;
	*)
		[ -z "$version" ] || die "only one version allowed"
		version="$1"
		;;
	esac
	shift
done

[ -n "$version" ] || usage
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "'$version' is not a release version (expected X.Y.Z)"
[[ "$pkgrel" =~ ^[1-9][0-9]*$ ]] || die "invalid pkgrel '$pkgrel'"

for tool in git makepkg updpkgsums; do
	command -v "$tool" > /dev/null || die "'$tool' not found (needs Arch Linux with base-devel and pacman-contrib)"
done

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(git -C "$script_dir" rev-parse --show-toplevel)"
aur_dir="${AUR_DIR:-$HOME/programming}"

info "Checking that tag $version exists on GitHub"
git -C "$repo_dir" ls-remote --exit-code --tags origin "refs/tags/$version" > /dev/null ||
	die "tag $version not found on origin; push it first"

if ! git -C "$repo_dir" diff --quiet HEAD -- packaging/aur; then
	die "packaging/aur has uncommitted changes; commit them first so the published files match the repository"
fi

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

# prepare_clone <pkgname>: makes sure $aur_dir/<pkgname> is an up-to-date, clean AUR clone
prepare_clone() {
	local pkg="$1" clone="$aur_dir/$1"
	if [ ! -d "$clone/.git" ]; then
		info "Cloning AUR repository $pkg into $clone"
		git -c init.defaultBranch=master clone "ssh://aur@aur.archlinux.org/$pkg.git" "$clone"
	fi
	if [ "$(git -C "$clone" config --get remote.origin.url)" != "ssh://aur@aur.archlinux.org/$pkg.git" ]; then
		die "$clone is not a clone of the AUR package $pkg"
	fi
	# a freshly created AUR package has no commits yet, so there is nothing to compare or pull
	if git -C "$clone" rev-parse --verify -q HEAD > /dev/null; then
		if ! git -C "$clone" diff --quiet HEAD --; then
			die "$clone has uncommitted changes to tracked files"
		fi
		info "Updating $clone"
		git -C "$clone" pull --ff-only --quiet
	elif [ -n "$(git -C "$clone" status --porcelain --untracked-files=no)" ]; then
		die "$clone has staged changes"
	fi
}

# build <pkgname>: generates .SRCINFO (and test-builds the package) in a temporary copy
build() {
	local pkg="$1" dir="$work_dir/$1"
	mkdir -p "$dir"
	cp "$script_dir/$pkg/PKGBUILD" "$script_dir/fan2go.install" "$dir/"
	(
		cd "$dir"
		if [ "$pkg" = "fan2go" ]; then
			sed -i "s/^pkgver=.*/pkgver=$version/; s/^pkgrel=.*/pkgrel=$pkgrel/" PKGBUILD
			info "$pkg: updating checksums"
			updpkgsums
		fi
		if [ "$skip_build" = true ]; then
			if [ "$pkg" = "fan2go-git" ]; then
				# runs pkgver() so the published pkgver is current
				makepkg --nobuild --nodeps --noprepare > /dev/null
			fi
		else
			info "$pkg: test build"
			makepkg --force --cleanbuild --noconfirm
		fi
		makepkg --printsrcinfo > .SRCINFO
	)
}

for pkg in fan2go fan2go-git; do
	prepare_clone "$pkg"
	build "$pkg"
	cp "$work_dir/$pkg/PKGBUILD" "$work_dir/$pkg/.SRCINFO" "$work_dir/$pkg/fan2go.install" "$aur_dir/$pkg/"
	git -C "$aur_dir/$pkg" add PKGBUILD .SRCINFO fan2go.install
done

changed=()
for pkg in fan2go fan2go-git; do
	if git -C "$aur_dir/$pkg" diff --cached --quiet; then
		info "$pkg: no changes"
	else
		changed+=("$pkg")
		echo
		info "$pkg: changes to publish"
		git -C "$aur_dir/$pkg" --no-pager diff --cached
	fi
done

if [ ${#changed[@]} -eq 0 ]; then
	info "Nothing to publish"
	exit 0
fi

if [ "$assume_yes" != true ]; then
	echo
	read -r -p "Publish ${changed[*]} for $version to the AUR? [y/N] " answer
	case "$answer" in
	y | Y | yes) ;;
	*)
		for pkg in fan2go fan2go-git; do
			if git -C "$aur_dir/$pkg" rev-parse --verify -q HEAD > /dev/null; then
				git -C "$aur_dir/$pkg" reset --quiet --hard
			else
				git -C "$aur_dir/$pkg" rm --quiet --cached --force -- PKGBUILD .SRCINFO fan2go.install
				rm -f "$aur_dir/$pkg/PKGBUILD" "$aur_dir/$pkg/.SRCINFO" "$aur_dir/$pkg/fan2go.install"
			fi
		done
		echo "Aborted, nothing was published. The AUR clones under $aur_dir were restored."
		exit 1
		;;
	esac
fi

for pkg in "${changed[@]}"; do
	info "$pkg: publishing"
	git -C "$aur_dir/$pkg" commit --quiet -m "Release $version"
	git -C "$aur_dir/$pkg" push origin HEAD:master
done

info "Done"
