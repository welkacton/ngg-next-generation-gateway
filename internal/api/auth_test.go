package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// The console must be unusable until an administrator exists, and the first
// run must be the only chance to create one without credentials.
func TestConsoleIsGatedByFirstRunSetupThenSession(t *testing.T) {
	ts, token := newTestServer(t)

	// 1) Anonymous console API calls are rejected.
	resp, err := http.Get(ts.URL + "/api/v1/system")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous /system: got %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// 2) The state endpoint is public and asks for setup.
	var state struct {
		SetupRequired bool `json:"setup_required"`
		Authenticated bool `json:"authenticated"`
	}
	getJSON(t, ts.URL+"/api/v1/auth/state", &state)
	if !state.SetupRequired || state.Authenticated {
		t.Fatalf("fresh install state = %+v", state)
	}

	// 3) Create the administrator; the response sets the session cookie.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	body := `{"username":"admin","password":"s3cret-pass"}`
	resp, err = client.Post(ts.URL+"/api/v1/auth/setup", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("setup: got %d %s", resp.StatusCode, raw)
	}
	resp.Body.Close()

	// 4) A second setup attempt must be refused.
	resp, err = client.Post(ts.URL+"/api/v1/auth/setup", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second setup: got %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()

	// 5) The cookie now opens the console.
	resp, err = client.Get(ts.URL + "/api/v1/system")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated /system: got %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// 6) Logout closes it again.
	if _, err := client.Post(ts.URL+"/api/v1/auth/logout", "application/json", nil); err != nil {
		t.Fatal(err)
	}
	resp, err = client.Get(ts.URL + "/api/v1/system")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout /system: got %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// 7) The static API token keeps working for scripts.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/system", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bearer /system: got %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestLoginRejectsBadPassword(t *testing.T) {
	ts, _ := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if resp, err := client.Post(ts.URL+"/api/v1/auth/setup", "application/json",
		strings.NewReader(`{"username":"admin","password":"s3cret-pass"}`)); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	client.Post(ts.URL+"/api/v1/auth/logout", "application/json", nil)

	resp, err := client.Post(ts.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"username":"admin","password":"wrong-pass"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login: got %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = client.Post(ts.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"username":"admin","password":"s3cret-pass","remember":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("good login: got %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}
