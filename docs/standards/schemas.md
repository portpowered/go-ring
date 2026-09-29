# API schema standard

- **SCHEMA-01** Describe Ring HTTP wire contracts in `api/openapi.yaml` and signaling contracts in `api/asyncapi.yaml`. Keep active third-party dependency routes in separate checked-in schemas under `api/external/`, and pin the source `.proto` for active binary exchanges.
- **SCHEMA-02** Describe public model projections in `api/client-models.openapi.yaml`. Do not use that file as a store for internal wire payloads.
- **SCHEMA-03** Give each observed request, response, event, and nested object a named schema. Use a free-form object only when the payload is truly unknown or extensible.
- **SCHEMA-04** State required fields, nullability, formats, bounds, units, and defaults when evidence supports them.
- **SCHEMA-05** Type known values as enums. Use an open enum for a value the service can extend. List known values without rejecting future values.
- **SCHEMA-06** Describe numeric ranges in the schema and its text. For example, a PTZ speed is from 0 through 1.
- **SCHEMA-07** Preserve unknown server fields where compatibility requires it. Do not invent a required field from one example.
- **SCHEMA-08** Record capture, synthetic, and reference provenance in fixture or contract documentation. Do not add `x-evidence` or `x-source` fields to API schemas.
- **SCHEMA-09** Prefer verified recording behavior when it differs from a reference library. Document the difference.
- **SCHEMA-10** Generate HTTP wire models and public projection models with `oapi-codegen` and signaling models with Modelina. Generate endpoint, channel, method, query, header, and wire-key constants from the checked-in HTTP, signaling, and external protocol schemas. Public SDK adapter types may remain handwritten when they supply behavior or custom decoding; their complete JSON field inventory and required fields must match a checked-in projection schema in a contract test. Generate binary socket endpoint and tag constants from their checked-in external protocol inventory. Do not hand-edit generated output; keep any inventory generator limited to syntax-checked constants rather than wire model definitions.
- **SCHEMA-11** Validate the full document and recorded payloads after a schema change. Keep negative cases for required fields, types, enums, and bounds.
- **SCHEMA-12** Keep private capture data and credentials out of the repository. Publish only reviewed, sanitized fixtures.
- **SCHEMA-13** Name reusable object schemas so generators can create stable Go types.
- **SCHEMA-14** Describe every OpenAPI path and operation in customer-facing terms.
- **SCHEMA-15** Describe the allowed shape of open strings. Add a pattern when one is known.
- **SCHEMA-16** Generate internal wire request, response, and event models from their checked-in schemas, including active dependency exchanges. Keep handwritten code for behavior and adaptation, and check regenerated output for drift in CI. A transport wrapper must reject dependency routes absent from the external schema.
