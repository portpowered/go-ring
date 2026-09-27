# Repository instructions

- Apply the standards in `docs/standards/` to every changed file.
- Read `docs/standards/go.md` before changing Go code or lint rules.
- Read `docs/standards/schemas.md` before changing an API schema or generated model.
- Read `docs/standards/client-api.md` before changing an exported client method or type.
- Read `docs/standards/library.md` before changing package boundaries, tests, fixtures, or documentation.
- Use a standard's rule ID in a change description when a decision needs explanation.
- Keep captured behavior separate from synthetic examples and historical references.
- Preserve unrelated local changes. Do not commit private captures, tokens, or generated executables.
- Run the checks that cover changed code. Run `make lint` and `make check` before proposing a completed change.
