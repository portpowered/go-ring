# RTC streaming procotol

The ring APIs uses a websocket connection as a signalling channel.
The signalling channel is used to establish the RTC session, as well as a keep alive connection.
When the signalling channel is closed, the RTC session is disconnected.


The connection logic can be viewed as roughly:
1. The customer calls the websocket ticket API to receive a ticket
2. The customer uses that ticket to establish a websocket connection
3. The customer uses that websocket connection to signal an SDP offer
4. The customer receives an SDP answer over the websocket connection
5. The customer persists the websocket connection and ping/pongs at a fixed cadence to maintain connection liveness
6. Both the websocket and the RTC connection are maintained and persisted for a fixed duration.
