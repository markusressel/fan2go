# Releasing fan2go

fan2go follows [semantic versioning](https://semver.org/) with the pre-1.0 convention: while the major version is 0,
a release with breaking changes bumps the **minor** version (`0.15.x` → `0.16.0`), everything else bumps the patch
version.

## During development: recording breaking changes

Every pull request has a **"Breaking changes / upgrade notes"** section (see
[the PR template](.github/pull_request_template.md)). A change is breaking if an existing user may notice it after
upgrading without changing anything: config semantics or defaults, CLI output or exit codes, the REST API or metrics,
persisted data, or how fans behave.

For a breaking change:

1. describe it in the PR's "Breaking changes / upgrade notes" section, written for users: what changes, who is
   affected, what they have to do, and
2. add the `breaking-change` label to the PR.

[Release Drafter](.github/release-drafter.yml) collects merged PRs into a draft release, lists PRs labeled
`breaking-change` in a "⚠️ Breaking changes" section and proposes the next version accordingly.

## Cutting a release

1. **Collect the upgrade notes.** List the merged PRs labeled `breaking-change` since the last release:

   ```shell
   gh pr list --state merged --label breaking-change --search "merged:>$(git log -1 --format=%cI $(git describe --tags --abbrev=0))" --json number,title,url
   ```

   Read their "Breaking changes / upgrade notes" sections.

2. **Add an upgrade warning for the Arch packages** if there are breaking changes. Append an entry for the new version
   at the end of the notes in [`packaging/aur/fan2go.install`](packaging/aur/fan2go.install):

   ```text
   == 0.16.0
   statistics.host now defaults to 127.0.0.1. Set `statistics.host: 0.0.0.0` to keep remote Prometheus scrapes working.
   onExit.speed is now mapped through the fan's pwmMap, like curve values.
   ```

   pacman shows these lines when a user upgrades across that version (both `fan2go` and `fan2go-git`). Keep each
   entry short and actionable; link to the release notes for details. Merge this change before tagging.

3. **Update the Makefile default version** (`VERSION ?=` in the [`Makefile`](Makefile)) to the new version, so local
   builds from the tag report it. CI and the Arch packages pass the version explicitly, so release artifacts are
   correct either way.

4. **Tag and push** from an up-to-date `master`:

   ```shell
   git tag 0.16.0
   git push origin 0.16.0
   ```

   This triggers:
   - the [Go workflow](.github/workflows/go.yml): builds the binaries (with the version from the tag) and attaches
     them to the GitHub release, and
   - the [AUR Packages workflow](.github/workflows/aur-packages.yml): test-builds the `fan2go` (from the tag's source
     tarball) and `fan2go-git` packages from [`packaging/aur`](packaging/aur). It doesn't publish anything.

5. **Publish the AUR packages** once the AUR Packages workflow for the tag has passed. On an Arch Linux machine with
   your AUR SSH key:

   ```shell
   packaging/aur/publish.sh 0.16.0
   ```

   The script updates your AUR clones (`$AUR_DIR/fan2go` and `$AUR_DIR/fan2go-git`, default `AUR_DIR=~/programming`;
   missing clones are created), sets `pkgver` and the checksum of the stable package, test-builds both packages in a
   temporary directory, shows the diff and pushes after you confirm. Answering "no" restores the clones. See
   `packaging/aur/publish.sh --help` for options (e.g. `--pkgrel 2` for a packaging-only fix of the same version).

6. **Publish the release notes.** Open the draft release created by Release Drafter, fill the "Upgrade notes"
   section from step 1 (or remove it if there are no breaking changes), check the version, and publish it.

## AUR packages

The PKGBUILDs live in [`packaging/aur`](packaging/aur) and are the source of truth; don't edit them on the AUR
directly, the next release overwrites them.

- `fan2go`: built from the release tarball. Its `pkgver` and checksum are set by `publish.sh`.
- `fan2go-git`: built from `master`. It contains unreleased changes, including breaking ones; its upgrade warnings
  only fire once the corresponding version has been tagged.

Publishing is deliberately manual (`packaging/aur/publish.sh`): an AUR SSH key grants push access to every package of
the account, and a PKGBUILD runs code on users' machines, so the key is not stored in GitHub.

Pull requests that change `packaging/aur` and release tags build both packages in CI.
