# Fixture provenance

These are sanitized response-shape fixtures inherited with the client source. Original capture dates and account provenance are unavailable, so they are not presented as verified live captures. Credentials, account/contact information, network identifiers, URLs, and opaque configuration have been replaced with synthetic values before publication. Device IDs used by assertions are fixture identifiers.

Unit tests use an injected HTTP transport and local WebSocket servers. These tests demonstrate client behavior against the supplied responses; they do not certify current vendor endpoint availability. Live integration tests are opt-in with the `integration` build tag. Never add unredacted credentials or recordings.
