package system_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

func TestSignalingSessionNegotiatesRoutesPTZAndCloses(t *testing.T) {
	var httpCalls int
	httpPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalls++
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clap/ticket/request/signalsocket" {
			t.Errorf("unexpected bootstrap request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ticket":"synthetic-ticket"}`))
	}))
	defer httpPeer.Close()
	serverErrors := make(chan error, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	wsPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "synthetic-ticket" {
			t.Errorf("ticket was not placed in signaling URL query")
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer conn.Close()
		fail := func(e error) { serverErrors <- e }
		read := func() (map[string]any, error) {
			_, b, e := conn.ReadMessage()
			if e != nil {
				return nil, e
			}
			var v map[string]any
			e = json.Unmarshal(b, &v)
			return v, e
		}
		write := func(v any) error {
			b, e := json.Marshal(v)
			if e != nil {
				return e
			}
			return conn.WriteMessage(websocket.TextMessage, b)
		}
		first, e := read()
		if e != nil {
			fail(e)
			return
		}
		if first["method"] != "live_view" {
			fail(fmt.Errorf("first method %v", first["method"]))
			return
		}
		dialog, _ := first["dialog_id"].(string)
		body, _ := first["body"].(map[string]any)
		if dialog == "" || body["doorbot_id"] != float64(1001) {
			fail(fmt.Errorf("bad live_view envelope"))
			return
		}
		offer, _ := body["sdp"].(string)
		if !strings.Contains(offer, "a=mid:0") {
			fail(fmt.Errorf("offer not sent"))
			return
		}
		if e = write(map[string]any{"method": "session_created", "dialog_id": dialog, "riid": "route-1", "body": map[string]any{"doorbot_id": 1001, "session_id": "signal-1"}}); e != nil {
			fail(e)
			return
		}
		if e = write(map[string]any{"method": "sdp", "dialog_id": dialog, "riid": "route-1", "body": map[string]any{"doorbot_id": 1001, "session_id": "signal-1", "type": "answer", "sdp": answerSDP, "session_info": map[string]any{"session_id": "control-1", "ping_interval": 10}}}); e != nil {
			fail(e)
			return
		}
		activation, e := read()
		if e != nil {
			fail(e)
			return
		}
		mic, e := read()
		if e != nil || mic["method"] != "mic_enable" {
			fail(fmt.Errorf("expected microphone setting: %v", e))
			return
		}
		options, e := read()
		if e != nil || options["method"] != "stream_options" {
			fail(fmt.Errorf("expected stream options: %v", e))
			return
		}
		if activation["method"] != "activate_session" {
			fail(fmt.Errorf("expected activate_session, got %v", activation["method"]))
			return
		}
		if e = write(map[string]any{"method": "camera_started", "dialog_id": dialog, "riid": "route-1", "body": map[string]any{"doorbot_id": 1001, "session_id": "signal-1"}}); e != nil {
			fail(e)
			return
		}
		for i, expected := range []string{"PTZ.Pan.Step", "PTZ.Pan.Step", "PTZ.Pan.Continuous"} {
			msg, e := read()
			if e != nil {
				fail(e)
				return
			}
			if msg["method"] != "rpc" {
				fail(fmt.Errorf("expected rpc, got %v", msg["method"]))
				return
			}
			mBody, _ := msg["body"].(map[string]any)
			cmd, _ := mBody["command"].(map[string]any)
			if cmd["method"] != expected {
				fail(fmt.Errorf("expected %s, got %v", expected, cmd["method"]))
				return
			}
			if mBody["session_id"] != "signal-1" {
				fail(fmt.Errorf("outer signal session ID missing"))
				return
			}
			params, _ := cmd["params"].(map[string]any)
			if params["sessionId"] != "control-1" {
				fail(fmt.Errorf("PTZ session ID domain missing"))
				return
			}
			if expected == "PTZ.Pan.Continuous" && params["speed"] != float64(0.5) {
				fail(fmt.Errorf("expected continuous speed, got %v", params["speed"]))
				return
			}
			if e = write(map[string]any{"method": "rpc", "dialog_id": dialog, "riid": "route-1", "body": map[string]any{"doorbot_id": 1001, "session_id": "signal-1", "command": map[string]any{"jsonrpc": "2.0", "id": cmd["id"], "result": map[string]any{"sessionId": "control-1", "timestamp": int64(1700000000001 + i), "version": 1}}}}); e != nil {
				fail(e)
				return
			}
		}
		stop, e := read()
		if e != nil {
			fail(e)
			return
		}
		stopBody, _ := stop["body"].(map[string]any)
		stopCommand, _ := stopBody["command"].(map[string]any)
		stopParams, _ := stopCommand["params"].(map[string]any)
		if stopCommand["method"] != "PTZ.Pan.Continuous" || stopParams["speed"] != float64(0) {
			fail(fmt.Errorf("close did not stop tracked movement: %v", stopCommand))
			return
		}
		if e = write(map[string]any{"method": "rpc", "dialog_id": dialog, "riid": "route-1", "body": map[string]any{"doorbot_id": 1001, "session_id": "signal-1", "command": map[string]any{"jsonrpc": "2.0", "id": stopCommand["id"], "result": map[string]any{"sessionId": "control-1", "timestamp": int64(1700000000010), "version": 1}}}}); e != nil {
			fail(e)
			return
		}
		closeMsg, e := read()
		if e != nil {
			fail(e)
			return
		}
		if closeMsg["method"] != "close" {
			fail(fmt.Errorf("expected close, got %v", closeMsg["method"]))
			return
		}
		serverErrors <- nil
	}))
	defer wsPeer.Close()
	wsURL := "ws" + strings.TrimPrefix(wsPeer.URL, "http") + "?token={token}"
	dialer := &captureDialer{delegate: websocket.DefaultDialer}
	client, err := ring.NewClient(ring.WithAccessToken("test-token"), ring.WithHTTPClient(httpPeer.Client()), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: httpPeer.URL}), ring.WithRTCWebSocketURL(wsURL), ring.WithWebSocketDialer(dialer))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dialer.url, "token=synthetic-ticket") || dialer.headers.Get("User-Agent") == "" {
		t.Fatalf("injected dialer missed endpoint or headers: %s", dialer.url)
	}
	session, err := conn.StartDeviceSession(context.Background(), ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: "offer", SDP: offerSDP}, VideoEnabled: true, ICEMode: ring.ICETrickle})
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Answer(); got.Type != "answer" || !strings.Contains(got.SDP, "a=sendonly") {
		t.Fatalf("invalid answer: %+v", got)
	}
	for _, candidate := range []ring.ICECandidateRequest{{Candidate: "candidate:x", MID: "unknown", MLineIndex: 0}, {Candidate: "candidate:x", MID: "0", MLineIndex: 1}} {
		if err = session.SendICE(context.Background(), candidate); err == nil {
			t.Fatalf("accepted candidate with mismatched media identity: %+v", candidate)
		}
	}
	for _, call := range []func(context.Context) (*ring.PTZResult, error){func(c context.Context) (*ring.PTZResult, error) {
		return session.PanStep(c, ring.PanStepRequest{Direction: "RIGHT"})
	}, func(c context.Context) (*ring.PTZResult, error) {
		return session.PanStep(c, ring.PanStepRequest{Direction: "LEFT"})
	}, func(c context.Context) (*ring.PTZResult, error) {
		return session.PanContinuous(c, ring.PanContinuousRequest{Direction: "RIGHT", Speed: 0.5})
	}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result, e := call(ctx)
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		if len(result.Raw) == 0 {
			t.Fatal("empty PTZ result")
		}
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err = session.Close(); err != nil {
		t.Fatalf("child close after parent teardown should be harmless: %v", err)
	}
	if httpCalls != 1 {
		t.Fatalf("ticket endpoint called %d times", httpCalls)
	}
	select {
	case e := <-serverErrors:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("local signaling peer did not complete")
	}
}

func TestTwoSessionsRouteRepliesByDialog(t *testing.T) {
	tickets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ticket":"fixture"}`))
	}))
	defer tickets.Close()
	serverErr := make(chan error, 1)
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := up.Upgrade(w, r, nil)
		if e != nil {
			serverErr <- e
			return
		}
		defer c.Close()
		read := func() (map[string]any, error) {
			_, b, e := c.ReadMessage()
			var v map[string]any
			if e == nil {
				e = json.Unmarshal(b, &v)
			}
			return v, e
		}
		write := func(v any) error {
			b, e := json.Marshal(v)
			if e != nil {
				return e
			}
			return c.WriteMessage(websocket.TextMessage, b)
		}
		var dialogs [2]string
		for i := 0; i < 2; i++ {
			m, e := read()
			if e != nil {
				serverErr <- e
				return
			}
			if m["method"] != "live_view" {
				serverErr <- fmt.Errorf("expected live_view")
				return
			}
			dialogs[i], _ = m["dialog_id"].(string)
			signal := fmt.Sprintf("signal-%d", i+1)
			control := fmt.Sprintf("control-%d", i+1)
			riid := fmt.Sprintf("route-%d", i+1)
			if e = write(map[string]any{"method": "session_created", "dialog_id": dialogs[i], "riid": riid, "body": map[string]any{"doorbot_id": 1001 + i, "session_id": signal}}); e != nil {
				serverErr <- e
				return
			}
			if e = write(map[string]any{"method": "sdp", "dialog_id": dialogs[i], "riid": riid, "body": map[string]any{"doorbot_id": 1001 + i, "session_id": signal, "type": "answer", "sdp": answerSDP, "session_info": map[string]any{"session_id": control, "ping_interval": 10}}}); e != nil {
				serverErr <- e
				return
			}
			activation, e := read()
			if e != nil || activation["method"] != "activate_session" {
				serverErr <- fmt.Errorf("expected activation: %v", e)
				return
			}
			for _, method := range []string{"mic_enable", "stream_options"} {
				setup, e := read()
				if e != nil || setup["method"] != method {
					serverErr <- fmt.Errorf("expected %s: %v", method, e)
					return
				}
			}
			if e = write(map[string]any{"method": "camera_started", "dialog_id": dialogs[i], "riid": riid, "body": map[string]any{"doorbot_id": 1001 + i, "session_id": signal}}); e != nil {
				serverErr <- e
				return
			}
		}
		type rpc struct {
			dialog, signal string
			control        string
			id             any
			device         int
		}
		calls := make([]rpc, 2)
		for i := 0; i < 2; i++ {
			m, e := read()
			if e != nil {
				serverErr <- e
				return
			}
			d, _ := m["dialog_id"].(string)
			body, _ := m["body"].(map[string]any)
			cmd, _ := body["command"].(map[string]any)
			idx := -1
			for j, x := range dialogs {
				if d == x {
					idx = j
				}
			}
			if idx < 0 {
				serverErr <- fmt.Errorf("unknown session dialog")
				return
			}
			calls[i] = rpc{d, fmt.Sprintf("signal-%d", idx+1), fmt.Sprintf("control-%d", idx+1), cmd["id"], 1001 + idx}
		}
		// A reply routed to the other dialog must not complete either call.
		a, b := calls[0], calls[1]
		if e = write(map[string]any{"method": "rpc", "dialog_id": b.dialog, "riid": "route-2", "body": map[string]any{"doorbot_id": a.device, "session_id": a.signal, "command": map[string]any{"jsonrpc": "2.0", "id": a.id, "result": map[string]any{"unexpected": true}}}}); e != nil {
			serverErr <- e
			return
		}
		for _, x := range []rpc{b, a} {
			if e = write(map[string]any{"method": "rpc", "dialog_id": x.dialog, "riid": "route-1", "body": map[string]any{"doorbot_id": x.device, "session_id": x.signal, "command": map[string]any{"jsonrpc": "2.0", "id": x.id, "result": map[string]any{"sessionId": x.control, "timestamp": 1700000000001, "version": 1}}}}); e != nil {
				serverErr <- e
				return
			}
		}
		for i := 0; i < 2; i++ {
			m, e := read()
			if e != nil || m["method"] != "close" {
				serverErr <- fmt.Errorf("expected child close: %v", e)
				return
			}
		}
		serverErr <- nil
	}))
	defer ws.Close()
	url := "ws" + strings.TrimPrefix(ws.URL, "http")
	client, e := ring.NewClient(ring.WithAccessToken("test-token"), ring.WithHTTPClient(tickets.Client()), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: tickets.URL}), ring.WithRTCWebSocketURL(url))
	if e != nil {
		t.Fatal(e)
	}
	conn, e := client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{})
	if e != nil {
		t.Fatal(e)
	}
	first, e := conn.StartDeviceSession(context.Background(), ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: "offer", SDP: offerSDP}, VideoEnabled: true})
	if e != nil {
		t.Fatal(e)
	}
	second, e := conn.StartDeviceSession(context.Background(), ring.StartDeviceSessionRequest{DeviceID: "1002", Offer: ring.SessionDescription{Type: "offer", SDP: offerSDP}, VideoEnabled: true})
	if e != nil {
		t.Fatal(e)
	}
	type result struct {
		value *ring.PTZResult
		err   error
	}
	results := make(chan result, 2)
	go func() {
		v, e := first.PanStep(context.Background(), ring.PanStepRequest{Direction: "LEFT"})
		results <- result{v, e}
	}()
	go func() {
		v, e := second.TiltStep(context.Background(), ring.TiltStepRequest{Direction: "DOWN"})
		results <- result{v, e}
	}()
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err != nil || r.value == nil {
				t.Fatalf("session RPC failed: %v", r.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("routed RPC did not complete")
		}
	}
	if e = first.Close(); e != nil {
		t.Fatal(e)
	}
	if e = second.Close(); e != nil {
		t.Fatal(e)
	}
	if e = conn.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-serverErr:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("two-session peer did not complete")
	}
}

func TestOpenContextClosesIdleSignalingSocket(t *testing.T) {
	tickets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"ticket":"fixture"}`)) }))
	defer tickets.Close()
	remoteClosed := make(chan struct{}, 1)
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := up.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		_, _, _ = c.ReadMessage()
		remoteClosed <- struct{}{}
	}))
	defer ws.Close()
	ctx, cancel := context.WithCancel(context.Background())
	url := "ws" + strings.TrimPrefix(ws.URL, "http")
	client, e := ring.NewClient(ring.WithAccessToken("test-token"), ring.WithHTTPClient(tickets.Client()), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: tickets.URL}), ring.WithRTCWebSocketURL(url))
	if e != nil {
		t.Fatal(e)
	}
	conn, e := client.OpenSignaling(ctx, ring.OpenSignalingRequest{})
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	done := make(chan error, 1)
	go func() { done <- conn.Close() }()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation left socket open")
	}
	select {
	case <-remoteClosed:
	case <-time.After(time.Second):
		t.Fatal("peer did not observe cancellation")
	}
}

const offerSDP = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=recvonly\r\n"
const answerSDP = "v=0\r\no=- 2 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=sendonly\r\n"

type captureDialer struct {
	delegate *websocket.Dialer
	url      string
	headers  http.Header
}

func (d *captureDialer) DialContext(ctx context.Context, url string, h http.Header) (*websocket.Conn, *http.Response, error) {
	d.url = url
	d.headers = h.Clone()
	return d.delegate.DialContext(ctx, url, h)
}
