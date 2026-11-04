// Package mobile is the shared Go core for Android / iOS / iPadOS / HarmonyOS
// clients. It is intentionally gomobile-bind friendly: exported types with
// string/error/bool parameters only. Native UI layers call these methods.
package mobile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const Version = "NGG-Stage5-mobile-core/1.0"

// Gateway is a thin, real REST client to the local/remote NGG control plane.
type Gateway struct {
	baseURL string
	token   string
	client  *http.Client
}

func New(baseURL, token string) *Gateway {
	return &Gateway{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (g *Gateway) SetToken(token string) { g.token = token }
func (g *Gateway) BaseURL() string       { return g.baseURL }
func (g *Gateway) CoreVersion() string   { return Version }

func (g *Gateway) call(method, path string, body any) (string, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, g.baseURL+"/api/v1/"+strings.TrimLeft(path, "/"), reader)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return string(b), nil
}

func (g *Gateway) SystemInfo() (string, error)    { return g.call("GET", "system", nil) }
func (g *Gateway) Cores() (string, error)         { return g.call("GET", "cores", nil) }
func (g *Gateway) NetworkStatus() (string, error) { return g.call("GET", "network/status", nil) }
func (g *Gateway) Stages() (string, error)        { return g.call("GET", "stages", nil) }
func (g *Gateway) SDWANStatus() (string, error)   { return g.call("GET", "sdwan/status", nil) }
func (g *Gateway) Protocols() (string, error)     { return g.call("GET", "protocols", nil) }

func (g *Gateway) StartCore(id string) (string, error) {
	return g.call("POST", "cores/"+id+"/start", nil)
}
func (g *Gateway) StopCore(id string) (string, error) {
	return g.call("POST", "cores/"+id+"/stop", nil)
}
func (g *Gateway) StartNetwork() (string, error) { return g.call("POST", "network/start", nil) }
func (g *Gateway) StopNetwork() (string, error)  { return g.call("POST", "network/stop", nil) }

func (g *Gateway) BatchStart(idsJSON string, exclusive bool) (string, error) {
	var ids []string
	if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
		return "", err
	}
	return g.call("POST", "cores/batch/start", map[string]any{"ids": ids, "exclusive": exclusive})
}

func (g *Gateway) SelfTest(coreID string) (string, error) {
	return g.call("POST", "network/selftest", map[string]string{"core_id": coreID})
}

func (g *Gateway) SetRules(rulesJSON string) (string, error) {
	var body map[string]any
	if err := json.Unmarshal([]byte(rulesJSON), &body); err != nil {
		return "", err
	}
	return g.call("PUT", "network/rules", body)
}

// ProxyURL returns the NGG ingress address for the platform VPN/proxy config.
func (g *Gateway) ProxyURL() (string, error) {
	status, err := g.NetworkStatus()
	if err != nil {
		return "", err
	}
	var v struct {
		Ingress struct {
			Address string `json:"address"`
		} `json:"ingress"`
	}
	if err := json.Unmarshal([]byte(status), &v); err != nil {
		return "", err
	}
	return v.Ingress.Address, nil
}

// Connectable performs a REAL HTTP request so a client can verify reachability
// before showing "connected" to the user (no simulated connectivity).
func (g *Gateway) Connectable() bool {
	_, err := g.call("GET", "health", nil)
	return err == nil
}
