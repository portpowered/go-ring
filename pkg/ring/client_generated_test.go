package ring

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/portpowered/go-ring/pkg/generatedapi"
)

func TestGeneratedHTTPClientRoutesAndServers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "wire" {
			t.Errorf("header=%q", r.Header.Get("X-Test"))
		}
		w.Header().Set("X-Path", r.URL.RequestURI())
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	client, err := NewClient(WithEndpoints(Endpoints{APIBaseURL: srv.URL, OAuthBaseURL: srv.URL, SolutionsBaseURL: srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, tc := range []struct {
		op    generatedapi.HTTPOperation
		input generatedapi.HTTPRequest
		want  string
	}{
		{generatedapi.OperationGetDevice, generatedapi.HTTPRequest{PathParams: map[string]string{"device_id": "7"}}, "/device_info/v3/devices/7"},
		{generatedapi.OperationRequestLegacySignalingTicket, generatedapi.HTTPRequest{}, "/api/v1/clap/ticket/request/signalsocket"},
		{generatedapi.OperationExchangeOrRefreshOAuthToken, generatedapi.HTTPRequest{}, "/oauth/token"},
	} {
		tc.input.Headers = http.Header{"X-Test": []string{"wire"}}
		if tc.op == generatedapi.OperationGetDevice {
			tc.input.Query = url.Values{"q": []string{"a b"}}
			tc.want += "?q=a+b"
		}
		resp, err := client.CallHTTP(context.Background(), tc.op, tc.input)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted || resp.Header.Get("X-Path") != tc.want {
			t.Fatalf("operation %s path=%q status=%d", tc.op, resp.Header.Get("X-Path"), resp.StatusCode)
		}
	}
	if _, err := client.CallHTTP(context.Background(), "missing", generatedapi.HTTPRequest{}); err == nil {
		t.Fatal("unknown operation accepted")
	}
	if _, err := client.CallHTTP(context.Background(), generatedapi.OperationGetDevice, generatedapi.HTTPRequest{}); err == nil {
		t.Fatal("missing path value accepted")
	}
	client.endpoints.APIBaseURL = ""
	if _, err := client.CallHTTP(context.Background(), generatedapi.OperationListDevices, generatedapi.HTTPRequest{}); err == nil {
		t.Fatal("missing API server accepted")
	}
}
