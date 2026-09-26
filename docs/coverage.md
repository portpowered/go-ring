# Coverage by test suite

Replay coverage is the primary Go compatibility signal. It measures maintained
handwritten library statements reached by `tests/replay` alone, using the same
sanitized captured and labeled synthetic fixtures used by the Python replay
harness. A passing replay test proves the behavior of that fixture and local
transport, not compatibility with an unrecorded device or live service.

Run `make test-cover` to produce three separate reports with the same 2,100
statement denominator:

| Suite | Test targets | Profile | Current coverage | CI floor |
| --- | --- | --- | ---: | ---: |
| Replay | `tests/replay` only | `coverage.replay.out` | 1,035/2,100 (49.29%) | 49% |
| Unit | Co-located tests in `pkg` and `internal` | `coverage.unit.out` | 1,583/2,100 (75.38%) | 75% |
| Combined | Replay and unit targets together | `coverage.combined.out` | 1,913/2,100 (91.10%) | 90%, plus per-package floors |

The replay floor preserves the current baseline; it is not the desired endpoint.
Replay coverage should rise as portable Python cases and captured interactions
become focused Go replay tests. The largest current replay gaps are REST/auth
adapters (31.8% replay coverage) and the public `ring` package (52.2%). A unit
test may cover these paths, but it does not raise the replay number. CI uploads
replay and unit profiles under separate Codecov flags so their contribution is
visible independently. The combined gate preserves the broader library budget;
it does not substitute for the replay gate.

All three Go reports use the same maintained-code denominator: handwritten
statements in `pkg` and `internal`. Generated HTTP/signaling code, generated
public models, testkit, examples, tests, and tools are excluded. The combined
run targets only replay and co-located unit tests, so examples or tool tests
cannot inflate it. The Python replay gate covers 447/465 **selected Python
executable lines** (96.13%). Its selection and line metric differ from the Go
all-maintained-code statement metric, so the percentages are not directly
comparable. Use the [porting matrix](porting-progress.md) to compare behavior
case by case, then use replay coverage to find unexercised Go paths.

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
