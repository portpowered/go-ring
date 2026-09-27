# Client interface standard

- **API-01** Present the supported client operations together in a single file for tracing.
- **API-02** Keep the public request and result types independent of REST and WebSocket wire structs.
- **API-03** Put `AuthContext` in each account request. Let one `Client` serve multiple accounts without changing shared authorization state.
- **API-04** Give session-opening requests an `AuthContext`. Bind each opened session to that account until it closes.
- **API-05** Ask for a device ID and the values the caller intends to change. Do not require a whole device object.
- **API-06** Hide endpoint-only fields such as family, kind, and description when a verified generic route or safe lookup supplies them.
- **API-07** Expose an endpoint-required field when no verified route or lookup can supply it. Explain the requirement in the field comment.
- **API-08** Use typed parameters for known enums. Keep server-extensible values forward compatible.
- **API-09** Use one public session object for the lifecycle and controls of a device connection. Give push and playback their own clear purpose.
- **API-10** Let the session own signaling, SDP and ICE exchange, ping and pong, PTZ commands, and termination. Make its state changes observable.
- **API-11** Distinguish command acknowledgement from completed physical action. Do not retry an uncertain movement command automatically.
- **API-12** Return typed errors for unauthorized, missing resource, unavailable snapshot, connection failure, timeout, and backpressure when the cause is known.
- **API-13** Keep the reusable client stateless. Put connection state in explicit session objects.
- **API-14** Return typed client errors. Do not expose raw transport errors by default.
- **API-15** Use named request and result structs for public operations. Avoid bare booleans or integers when their meaning needs context.
- **API-16** Prefer one device abstraction across device families.
- **API-17** Model capabilities independently of device family or type.
