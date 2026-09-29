# Go code standard

- **GO-01** Run `gofmt` on every changed Go file.
- **GO-02** Keep each package focused on one responsibility. Keep transport logic out of the client interface package.
- **GO-03** Give every exported type, method, and field a clear purpose. Remove unused public options.
- **GO-04** Use a named type for each meaningful request, response, state, or protocol value. Avoid anonymous structs in interfaces and persistent state.
- **GO-05** Use typed constants for fixed protocol values. Store service URLs, paths, JSON names, and RPC names in `internal/protocol` or generated code.
- **GO-06** Store time limits and queue sizes with the component that enforces them. Give each non-obvious number a named constant.
- **GO-07** Return typed errors that preserve the cause and let callers test the failure class. Do not replace a useful cause with an untyped message.
- **GO-08** Accept `context.Context` for cancellable work. End owned goroutines when the context or owner closes.
- **GO-09** Define which object owns each channel, connection, timer, and close operation. Make `Close` safe to call more than once.
- **GO-10** Do not hold a lock while calling user code or waiting on network I/O. Test concurrent close, send, timeout, and failure paths with `-race`.
- **GO-11** Keep functions and files short enough to review. Follow the configured complexity and length limits; split code by responsibility.
- **GO-12** Keep unit tests beside their source files. Put offline API replay tests in `tests/replay` and live endpoint tests in `tests/integration`.
- **GO-13** Keep generated files generated. Change their schema or generator input, then regenerate.
- **GO-14** Run static checks and tests after a change. Do not silence a linter for a whole package to avoid fixing handwritten code.
- **GO-15** Enforce stable code rules with the pinned `golangci-lint` version in blocking CI. Set `linters.default: all` and run every repository Go module; do not use new-issues baselines or failure bypasses. Keep all linters enabled. Fix each finding, or document a justified exception for one linter on an exact generated path, fixture type, or named file. Require an independent review that approves the exact commit SHA after its CI checks pass.
