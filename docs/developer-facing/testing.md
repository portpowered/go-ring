# Testing strategy

The library uses a three pronged testing strategy
1. unit tests for individual components
2. replay/functional tests that test the library independently
3. integration tests that test the system e2e.

We prefer:
1. small unit tests
2. BIG replay/functional tests
3. tiny integration tests

## unit tests
unit tests are intended to cover logically complex objects that are hard to deal with independently.
i.e.
1. preparation of SDP offers
2. internal logical transformations and validation

## replay tests
replay tests are intended to be set of library agnostic harnesses that a customer can use to re-port a library to say zig/rust/cpp/whatever a customer wants, without having to go through the whole process over again.

We basically record a bunch of network data streams, and test that our system can evaluate and call that API traffic for say, an invocation to reboot a device or whatever else.

## integration tests

This actually tests the service with some mock credentials to test the thing works e2e.
