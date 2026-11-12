# Notes for contributors and coding agents

## Design documentation

The [`docs/`](docs/README.md) folder describes how fan2go's core systems work and why: design decisions,
alternatives, defaults, config keys and known limitations.

- When a change affects one of these systems, update the matching document in the same pull request. This includes
  new or changed behaviour, defaults, config keys, fixed limitations and new known limitations.
- Before a release, check the documents against the code and fix any drift (see [RELEASING.md](RELEASING.md)).

## Pull requests

Fill in every section of the [pull request template](.github/pull_request_template.md). The "Breaking changes /
upgrade notes" section is required; see [RELEASING.md](RELEASING.md) for how it is used.
