# Contributing

Use Go 1.24 or newer. Follow the repository instructions in [AGENTS.md](AGENTS.md)
and the standards in [docs/standards/](docs/standards/). Use the
[release checklist](docs/template-checklist.md) and maintain its
[independent review record](docs/independent-review.md).

Before submitting a pull request, run:

```sh
make check
make lint
make test-race
make build-examples
```

Test transport behavior with paired replay fixtures or local HTTP and WebSocket
servers. Keep captured, source-derived, synthetic, and historical examples
clearly labeled; do not add private captures, credentials, device addresses,
account details, or recordings. See the
[fixture guide](tests/replay/fixtures/README.md) for the fixture format and
evidence labels. Live tests must use the `integration` build tag and remain
optional; `make test-integration` requires `RING_ACCESS_TOKEN`. Use
`make test-cover` to report replay, unit, and combined coverage separately.

Keep public changes compatible where practical. Add Go documentation and
focused behavioral tests for new public APIs, update runnable examples and the
relevant [customer guides](docs/guides/), and review the [client API
standard](docs/standards/client-api.md) before changing exported types or
methods. Before an intentional API break or release, review the compatibility
output from CI and use a semver version that allows the change. Describe
behavior changes and verification in the pull request.
