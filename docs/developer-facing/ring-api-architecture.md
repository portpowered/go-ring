# Ring API architecture

## Authentication/Authorization

Ring API uses a mostly standard PKCE/OAUTH2 style API for generating auth tokens.
It generates JWT tokens for authz.

CORS isn't available.
Auth generally requires MFA, so secondary auth users needs to provide for OTP and others.

## API
Largely the Ring APIs are composed of three API shapes.
1. stateless RESTful APIs
2. Websocket connections for bidirectional comms
3. WebRTC for live video streams

### RESTful APIs

Ring API enables customers to enumerate the devices that a customer has.

The API is mostly stateless and allows customers to amongst other things:
1. figure out the battery state/network connectivity/power of a device
2. figure out the names/homes of devices
3. check the device information for various things
4. etc.

### WebRTC/Session streaming

Ring API connection to a device is done statefully.

1. A customer establishes a websocket signalling connection to the ring API.
2. Using that signalling connection, customer can then create a device connection.
3. The customer uses the websocket device connection to do things like have live view, move the camera left/right, enable audio, etc.
4. To enable live view the websocket uses WebRTC. We pass in a webRTC SDP connection, as well as support trickle ICE and establish a webrtc session between the endpoint and our system.
