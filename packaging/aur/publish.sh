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
#   --yes           don't ask for confirmation: push, also if the checkout is not the latest master
#
# Environment:
#   AUR_DIR         directory containing the AUR clones fan2go/ and fan2go-git/ (default: ~/programming/AUR);
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

warn() {
	echo "warning: $*" >&2
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
aur_dir="${AUR_DIR:-$HOME/programming/AUR}"

info "Checking that tag $version exists on GitHub"
git -C "$repo_dir" ls-remote --exit-code --tags origin "refs/tags/$version" > /dev/null ||
	die "tag $version not found on origin; push it first"

if ! git -C "$repo_dir" diff --quiet HEAD -- packaging/aur; then
	die "packaging/aur has uncommitted changes; commit them first so the published files match the repository"
fi

# confirm <question>: returns 0 if the user answers yes (or --yes was given)
confirm() {
	local answer=""
	if [ "$assume_yes" = true ]; then
		return 0
	fi
	read -r -p "$1 [y/N] " answer || answer=""
	case "$answer" in
	y | Y | yes) return 0 ;;
	*) return 1 ;;
	esac
}

# the stable package is published with the PKGBUILD of this checkout, so it should be the latest master
default_branch=master
info "Checking that the checkout is the latest $default_branch"
git -C "$repo_dir" fetch --quiet origin "$default_branch" || die "could not fetch $default_branch from origin"
branch="$(git -C "$repo_dir" symbolic-ref --quiet --short HEAD || echo "a detached HEAD")"
behind="$(git -C "$repo_dir" rev-list --count HEAD..FETCH_HEAD)"
unpushed=false
git -C "$repo_dir" diff --quiet FETCH_HEAD HEAD -- packaging/aur || unpushed=true
if [ "$branch" != "$default_branch" ] || [ "$behind" -gt 0 ] || [ "$unpushed" = true ]; then
	if [ "$branch" != "$default_branch" ]; then
		warn "$repo_dir is on $branch, not on $default_branch"
	fi
	if [ "$behind" -gt 0 ]; then
		warn "$repo_dir is $behind commit(s) behind origin/$default_branch"
	fi
	if [ "$unpushed" = true ]; then
		warn "packaging/aur in $repo_dir differs from origin/$default_branch"
	fi
	confirm "Publish with the packaging files of this checkout anyway?" ||
		die "aborted; check out $default_branch and pull first"
fi

work_dir="$(mktemp -d)"
# AUR clones this script has changed and not published; restored if it stops (error or "no")
touched=()
publishing=false
# HEAD of each AUR clone before this script changed it (empty for a clone without commits)
declare -A orig_head=()

# restore_clones: undoes the changes of this script in the AUR clones in $touched
restore_clones() {
	local pkg clone
	for pkg in "${touched[@]}"; do
		clone="$aur_dir/$pkg"
		if [ -n "${orig_head[$pkg]:-}" ]; then
			# also drops a commit of this script that was not pushed
			git -C "$clone" reset --quiet --hard "${orig_head[$pkg]}"
			# a file that is new in this release (and was not staged yet) is not removed by the reset
			if ! git -C "$clone" ls-files --error-unmatch fan2go.install > /dev/null 2>&1; then
				rm -f "$clone/fan2go.install"
			fi
		else
			# drops a first commit of this script that was not pushed
			git -C "$clone" update-ref -d HEAD 2> /dev/null || true
			git -C "$clone" rm --quiet --cached --force --ignore-unmatch -- PKGBUILD .SRCINFO fan2go.install
			rm -f "$clone/PKGBUILD" "$clone/.SRCINFO" "$clone/fan2go.install"
		fi
	done
	touched=()
}

cleanup() {
	local status=$?
	set +e
	if [ ${#touched[@]} -gt 0 ]; then
		if [ "$status" -eq 0 ]; then
			:
		elif [ "$publishing" = true ]; then
			echo "error: not published: ${touched[*]}. Restoring their AUR clones." >&2
		else
			echo "error: stopped before publishing, nothing was published. Restoring the AUR clones: ${touched[*]}" >&2
		fi
		restore_clones
	fi
	rm -rf "$work_dir"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

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
	orig_head[$pkg]="$(git -C "$clone" rev-parse --verify -q HEAD || true)"
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
	touched+=("$pkg")
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

echo
if ! confirm "Publish ${changed[*]} for $version to the AUR?"; then
	restore_clones
	echo "Aborted, nothing was published. The AUR clones under $aur_dir were restored."
	exit 1
fi

# from here on, only the clones of packages that were not pushed (yet) are restored
touched=("${changed[@]}")
publishing=true
for pkg in "${changed[@]}"; do
	info "$pkg: publishing"
	git -C "$aur_dir/$pkg" commit --quiet -m "Release $version"
	# on failure, the restore drops the commit, otherwise the next run would find nothing to publish
	git -C "$aur_dir/$pkg" push origin HEAD:master || die "pushing $pkg failed"
	touched=("${touched[@]:1}")
done

info "Done"
