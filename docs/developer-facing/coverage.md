# Coverage by test suite

Run `make test-cover` for the replay, unit, and combined reports. The reports
measure the same maintained Go statements in `pkg` and `internal`. Generated
wire and public models, testkit, examples, tests, and tools are excluded.

| Suite | Profile | Current coverage | CI floor |
| --- | --- | ---: | ---: |
| Replay | `coverage.replay.out` | 3,048/3,579 (85.16%) | 85% |
| Unit | `coverage.unit.out` | 2,476/3,579 (69.18%) | 50% |
| Combined | `coverage.combined.out` | 3,338/3,579 (93.27%) | 90%, plus package floors |

These values come from offline runs on 2026-09-29. Replay tests drive the
public API against local HTTP, WebSocket, and MCS peers with captured or
labeled synthetic fixtures. They validate those exchanges, not unrecorded
accounts, devices, or regions. The combined gate also includes co-located unit
tests; it does not replace the replay gate. Current combined package coverage
is 91.0% for WebSocket, 95.0% for REST, 87.8% for push, and 92.8% for `pkg/ring`.

CI uploads replay and unit profiles separately to Codecov. On `main`, the
replay profile also produces the README badge and HTML report in the Wiki.

For other checks, run:

```sh
go test -race ./... -timeout 120s
go vet ./...
make lint
make test-stress
make test-contracts
```

`make test-stress` repeats adversarial signaling and session lifecycle cases
under the race detector. `make test-contracts` checks schema and fixture
contracts. Set `GOWORK=off` when testing outside a parent workspace. The
private mitmproxy capture is not required.

Live coverage is optional and separate: `make test-cover-integration` runs
`tests/integration` with the `integration` build tag and writes
`coverage.integration.out`. It requires `RING_ACCESS_TOKEN`. Live tests may
skip when the account has no applicable device or recording, so inspect their
output alongside the report.
