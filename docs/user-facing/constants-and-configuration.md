# Client

## What is all the available functions.

The available set of functions is denoted inside of the pkg/ring/interfaces.go function.

## how to configure the client

The client is instantiated with a series of options that modify how the customer is configured

for example.

```
ring.NewClient(ring.WithRegion(ring.RegionUS))
```

Then you can chain all the various options that you want such as for setting up the http client or the dialer and what not.

All the options are under pkg/ring/client_options

## retries/metrics/throttles/clients

Sometimes you want to inject retries/metrics/throttles on the client network level.

This can be done via the http.RoundTripper on the http.Client level. Simply inject whatever you want on the http client
and mathc it that way.
