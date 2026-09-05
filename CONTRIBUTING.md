# Contributing

Use Go 1.24 or newer. Run `make check`, `make test-race`, and `make build-examples` before submitting a pull request. CI runs without a parent workspace or vendor credentials.

Test at the transport seam with explicit synthetic fixtures or local HTTP/WebSocket servers. Live tests must use the `integration` build tag. Do not include account credentials, private captures, device addresses, or recordings in contributions. Document the origin and redaction of any new fixture.

Keep public changes compatible where practical, update examples, and describe behavior changes and verification in the pull request. New public APIs need Go documentation and focused behavioral tests. Maintainer: PortPowered.
