# A good golang library for 3P sdks

For GOLANG SDK libraries

API best practices:

1. abstract out internal asynchronous internals (no direct co routines or whatever)
2. context is passed in as a direct parameter
3. struct request parameters, struct response objects
4. thin APIs as much as possible
5. API in a single file that can be retrieved
6. allow injection via wires of optional parameters
    1. the target API network clients (i.e. for the websocket connection the HTTP endpoint/client, etc)
7. constants declared in a separate file containing all relevant endpoints version
8. Keep it medium level
    1. abstract out system internals, put up abstractions
    2. abstract out versions of system internals when appropriate, don’t expose the variants
9. Device specific API abstractions
    1. treat devices generally when possible, to allow maximal extensibility
        1. i.e. use a capabilities based model o ffunctionality
10. Auth
    1. auth should be injectable via direct parameters, or via a runtime injector
    2. generating auth keys and persisting them should not be the purview of the librari
    3. if we do need to refresh keys for the duration of a session, we should either push a callback that can retrieve the referesh auth keys + enable customres to propagate their own auth keys via their own retry mechanisms
11. Statefulness
    1. Systems should be stateless as much as possible and when it is stateful we should leave it out
    2. if it must be stateful, we should expose a state sessoin object that maps and propagates when it fails/terminates, when it has state, etc.
12. errors
    1. we should have custom error type for all the errors
13. logging/metrics
    1. these are things that should be captured by the client
14. retries/throttling
    1. these are to be configured at the http/network injection layer ,not something we configure by default?

Library best practices:

1. documentation
    1. API is documented in appropriate formats
        1. websockets are modelled in async API
        2. REST is modelled in OpenAPI
        3. GraphQL is modelled as a raw GQL file.
        4. bespoke protocols are documented in whatever is appropriate (GRPC, custom openAPI, wtv)
        5. constants for API endpoints should be denoted, and if regional we should denote each
        6. our code should try to codegen as much as possible from the schema files
    2. Architecture notes
        1. Denote how auth works, and what is needed (is it sessional, how does it do retry/permissions)
        2. Denote how APIs are used (what parts are sessional, how do different functionality work)
        3. Denote how different devices are modelled, etc. how does capabilities work for differentiation etc
        4. gotchas in the system, and how to handle statefulness/session states are important
    3. feature sets
        1. we should document generally which devices we support, which functionality we support etc, so that customers can know how things work
    4. references
        1. we should try to keep references of all the stuff that was built into how this works
        2. how we go about reverse engineering something should be documented generally
        3. experimental notes such as how you go about doing network capture should be recorded
2. testing
    1. testing should prefer to have coverage measures and target 90%+ coverage
        1. reduce code when possible
    2. testing should be replay test based (i.e. do network capture, and sanitize the network capture, then use those as the sample data that we use to test things)
    3. traces should be recorded and pointed to as part of the documentation
3. operational best practices
    1. we should denote in the library how to do things such as injecting retries/clients/etc.
        1. we should prefer that systems should inject at the rountripper/http client or the websocket dialer/rpc client level rather than having something activated at the library
    2. logging
        1. we prefer to keep logging minimal, and if we need it with an option we use slog by default
4. network
    1. denote whether CORS is supported ont he endpoints directly
    2. denote
5. examples
    1. we should show examples of how a library works

CI:
1. automated builds and CI/test on each release, builds on windows/mac/linux
2. proper release versioning system
3. generated code coverage documentation

AGENTS.md
1. short and to the point (languages, systems)
2. defines the high level architecture (level of abstraction, UX intent, etc)
3. points at intended customer experience in the README.md

LICENSE:
1. apache-2

README best practices:

1. list what it is
2. show badges (golang version ,testing coverage, releases, ci status)
3. list how to use it
4. link to the API that the system has
5. list features set
    1. device API bespoke
        1. supported features
        2. cross matrix of tested feature versus fucntioanlity
6. link to relevant docs
    1. systems architecture (in terms of 3P APIs)
    2. library architecture (in terms of level of abstraction)
    3. reverse engineering guide
7. list the license
8. the coverage marks badge should be generated via the ncruces library, should as much as possible use out of the box supported badges when possible.
