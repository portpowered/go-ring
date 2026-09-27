# Public Go API compatibility

CI compares the public packages `pkg/ring` and `pkg/ringapimodels` with the
pull request base commit and writes any incompatible changes to the check
summary. The report is nonblocking so an intentional v0 minor release can be
reviewed and merged. Reviewers must explicitly acknowledge any reported API
break before approving the pull request.

The release workflow compares the release tag with the latest earlier stable
`vMAJOR.MINOR.PATCH` tag. A first release has no baseline and skips the
comparison. The release check fails if incompatible changes are tagged as a
patch release. For this v0 module, an incompatible change is allowed with an
increased minor version; a major version increase also permits it. The release
tag must be newer than the selected baseline even when the API is compatible.
The release summary lists any accepted breaks for review.

The check uses the Go team's `apidiff` tool to report source compatibility
changes. It does not detect behavioral changes, and it does not compare
transport packages, tests, tools, or the separate CLI module. Replay tests
remain the primary behavioral compatibility measure (LIB-05).

Run the check locally from the repository root with a Git ref as the baseline:

```sh
go run ./tools/compatibility -base origin/main
```

For a release comparison, pass `-base previous-release` and the current tag
with `-version`, as the release workflow does. The checker temporarily adds a
detached Git worktree for the baseline and removes it after the comparison.

The compatibility tool version is pinned in
`tools/compatibility/main.go`. Updating it should be a separate, reviewable
tooling change. The current tool pin requires Go 1.26; the checker enables Go's
automatic toolchain selection for the comparison tool while the library keeps
its Go 1.24 minimum.
