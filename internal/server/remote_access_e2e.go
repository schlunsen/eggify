//go:build eggify_e2e

package server

// End-to-end test hooks, compiled only with `-tags eggify_e2e` (never in a
// release build):
//
//   EGGIFY_E2E_NGROK_API_URL     ngrok REST API base URL (a local fake)
//   EGGIFY_E2E_TUNNEL_PROVIDER   "fake": a loopback listener poses as the
//                                tunnel. While the fake API lists 3 or more
//                                agent sessions it fails with ERR_NGROK_108,
//                                like ngrok on a free account; on success it
//                                registers its session (with metadata) at
//                                POST <api>/__fake/sessions.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/schlunsen/eggify/internal/remoteaccess"
)

func init() {
	apiURL := os.Getenv("EGGIFY_E2E_NGROK_API_URL")
	if apiURL != "" {
		newNgrokAPI = func() *remoteaccess.NgrokAPI { return &remoteaccess.NgrokAPI{BaseURL: apiURL} }
	}
	if os.Getenv("EGGIFY_E2E_TUNNEL_PROVIDER") == "fake" {
		newRemoteAccessProvider = func() remoteaccess.Provider { return &e2eProvider{api: apiURL} }
	}
}

type e2eProvider struct{ api string }

func (p *e2eProvider) Name() string { return "ngrok" }

type e2eListener struct {
	net.Listener
	api, id string
}

func (l *e2eListener) URL() string    { return "https://eggify-e2e.ngrok-free.app" }
func (l *e2eListener) Region() string { return "eu" }
func (l *e2eListener) Close() error {
	if l.id != "" && l.api != "" {
		req, _ := http.NewRequest(http.MethodDelete, l.api+"/__fake/sessions/"+l.id, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}
	return l.Listener.Close()
}

func (p *e2eProvider) Open(ctx context.Context, oc remoteaccess.OpenConfig) (remoteaccess.Listener, error) {
	if p.api != "" {
		api := &remoteaccess.NgrokAPI{BaseURL: p.api}
		list, err := api.ListTunnelSessions(ctx, "e2e-provider-key")
		if err == nil && len(list) >= 3 {
			return nil, &remoteaccess.PermanentError{
				Code: remoteaccess.ErrCodeAgentLimit,
				Err: errors.New("ngrok: Your account is limited to 3 simultaneous ngrok agent sessions (ERR_NGROK_108). " +
					"Other ngrok agents are using all of your account's sessions: stop one at dashboard.ngrok.com/agents " +
					"(or from Eggify with an ngrok API key) and try again. Sessions that ended recently can take a minute to expire"),
			}
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	l := &e2eListener{Listener: ln, api: p.api}
	if p.api != "" {
		body, _ := json.Marshal(map[string]string{"metadata": oc.Metadata})
		resp, err := http.Post(p.api+"/__fake/sessions", "application/json", bytes.NewReader(body))
		if err == nil {
			var out struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&out)
			resp.Body.Close()
			l.id = out.ID
		} else {
			fmt.Fprintf(os.Stderr, "e2e: register session: %v\n", err)
		}
	}
	return l, nil
}
