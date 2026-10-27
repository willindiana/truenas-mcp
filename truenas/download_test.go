package truenas

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

type downloadRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params []interface{}   `json:"params"`
}

// newDownloadFake serves JSON-RPC on apiPath and plain HTTP downloads elsewhere.
// reply returns a result or a JSON-RPC error object for non-login methods.
func newDownloadFake(t *testing.T, download http.HandlerFunc, reply func(downloadRequest) (interface{}, map[string]interface{})) *Client {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != apiPath {
			download(w, r)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for {
			var req downloadRequest
			if conn.ReadJSON(&req) != nil {
				return
			}
			resp := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
			if req.Method == "auth.login_ex" {
				resp["result"] = map[string]interface{}{"response_type": "SUCCESS"}
			} else if result, rpcErr := reply(req); rpcErr != nil {
				resp["error"] = rpcErr
			} else {
				resp["result"] = result
			}
			if conn.WriteJSON(resp) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClientWithOptions("wss"+strings.TrimPrefix(server.URL, "https")+apiPath, testAPIKey,
		&tls.Config{InsecureSkipVerify: true}, Options{APIUser: testAPIUser, Proxy: "none"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func downloadRPCError(reason string) map[string]interface{} {
	return map[string]interface{}{"code": -32001, "message": "Method call error", "data": map[string]interface{}{"error": 2, "reason": reason}}
}
