# Notes for contributors and coding agents

## Design documentation

The [`docs/`](docs/README.md) folder describes how fan2go's core systems work and why: design decisions,
alternatives, defaults, config keys and known limitations.

- When a change affects one of these systems, update the matching document in the same pull request. This includes
  new or changed behaviour, defaults, config keys, fixed limitations and new known limitations.
- Before a release, check the documents against the code and fix any drift (see [RELEASING.md](RELEASING.md)).

## Known hardware quirks

Device-specific behaviour goes into the registry in `internal/quirks/registry.go`, never into heuristics. See
[docs/hardware-quirks.md](docs/hardware-quirks.md#adding-an-entry):

- Identify the device exactly (hwmon `name`, optionally a modalias glob).
- Back every behaviour of an entry with a primary source: a kernel doc or driver source link, pinned to a commit,
  first in `Sources`. Behaviour that is only known from a user report is documented as a config recipe instead.
- Increment the entry's `Version` on every change of its readback formula (it triggers a new analysis of the affected
  fans).
- Add a fake sysfs tree under `internal/hwmon/testdata/sysfs/`, tests for formulas and advice, and a row in the docs.

## Pull requests

Fill in every section of the [pull request template](.github/pull_request_template.md). The "Breaking changes /
upgrade notes" section is required; see [RELEASING.md](RELEASING.md) for how it is used.
