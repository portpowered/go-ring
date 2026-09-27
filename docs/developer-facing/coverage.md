# Coverage by test suite

## How to run
Tests have one layout: `tests/replay` exercises the public API against local
HTTP/WebSocket peers driven by checked-in captured or explicitly synthetic
fixtures; `tests/integration` contains opt-in tests against real endpoints.
Small white-box unit tests live beside their implementation in `pkg` or
`internal`. Replay cases isolate session establishment, ICE, PTZ replies,
heartbeat, and termination; the full captured conversation remains an ordering
regression test.

```sh
go test -race ./... -timeout 120s
go vet ./...
go build ./examples/...
make lint
make test-stress
```

`make test-stress` repeats the adversarial state-machine cases under the race
detector. They race RPC correlation, expiry, explicit close, connection
failure, push backpressure, playback/push heartbeat, and continuous PTZ
controls. The assertions require bounded completion and released child/RPC
registries; a higher statement percentage alone does not establish those
properties.

Set `GOWORK=off` when testing this module independently of a surrounding workspace.
The fixture-contract checks use `tools/protocols` and `tools/capture`. Install
their pinned requirements and run `python -m unittest discover -s tools/protocols`
and `python -m unittest discover -s tools/capture`. The private mitmproxy file
is not required.

`make test-cover` measures replay, unit, and their combined coverage separately.
Replay is the primary compatibility metric: the local account-scope replay run
reaches 85.92% of maintained Go statements; unit tests reach 56.66%, and their
union reaches 93.00%. These suites have separate CI floors and profiles. Live
tests are opt-in via `make test-integration`, with separate coverage through
`make test-cover-integration`.

The README badge and linked HTML report are published from
`coverage.replay.out` by [go-coverage-report](https://github.com/ncruces/go-coverage-report)
on pushes to `main`. The badge and report are stored in the repository Wiki.


## implementation:
Replay coverage is the primary Go compatibility signal. It measures maintained
handwritten library statements reached by `tests/replay` alone, using
sanitized captured and labeled synthetic fixtures. A passing replay test proves the behavior of that fixture and local
transport, not compatibility with an unrecorded device or live service.

Run `make test-cover` to produce three separate reports with the same 2,529
statement denominator:

| Suite | Test targets | Profile | Current coverage | CI floor |
| --- | --- | --- | ---: | ---: |
| Replay | `tests/replay` only | `coverage.replay.out` | 2,173/2,529 (85.92%) | 85% |
| Unit | Co-located tests in `pkg` and `internal` | `coverage.unit.out` | 1,433/2,529 (56.66%) | 50% |
| Combined | Replay and unit targets together | `coverage.combined.out` | 2,352/2,529 (93.00%) | 90%, plus per-package floors |

The replay floor preserves the current baseline; it is not the desired endpoint.
Replay coverage should rise as captured and synthetic interactions become
focused Go replay tests. A unit
test may cover these paths, but it does not raise the replay number. CI uploads
replay and unit profiles under separate Codecov flags so their contribution is
visible independently. The combined gate preserves the broader library budget;
it does not substitute for the replay gate.

All three Go reports use the same maintained-code denominator: handwritten
statements in `pkg` and `internal`. Generated HTTP/signaling code, generated
public models, testkit, examples, tests, and tools are excluded. The combined
run targets only replay and co-located unit tests, so examples or tool tests
cannot inflate it. Use the [migration test mapping](../plans/porting-progress.md)
to compare behavior case by case, then use replay coverage to find
unexercised Go paths.

Live tests have their own opt-in report:

```sh
make test-cover-integration
```

This runs only `tests/integration` with the `integration` build tag and writes
`coverage.integration.out`. It requires `RING_ACCESS_TOKEN`; without it the
command fails before running, because skipped tests would produce a misleading
zero-coverage report. Live coverage has no CI floor and is not merged into the
offline numbers. Individual integration tests may still skip when the account
has no applicable device or recording; inspect their output with the report.
