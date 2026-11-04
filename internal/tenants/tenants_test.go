package tenants

import (
	"context"
	"testing"

	"github.com/ngg-project/ngg/internal/netengine"
	"github.com/ngg-project/ngg/pkg/contracts"
)

func TestTenantIsolation(t *testing.T) {
	r := NewRegistry()
	if id, err := r.ResolveTenant(context.Background(), ""); err != nil || id != "default" {
		t.Fatalf("default tenant: %q %v", id, err)
	}
	if err := r.Add(Tenant{ID: "acme", Name: "Acme", Enabled: true, AllowedCores: []string{"mihomo"}, QuotaBytes: 100}); err != nil {
		t.Fatal(err)
	}
	if !r.Allowed("acme", netengine.RouteDecision{CoreID: contracts.CoreMihomo}) {
		t.Fatal("allowed core must pass")
	}
	if r.Allowed("acme", netengine.RouteDecision{CoreID: contracts.CoreXray}) {
		t.Fatal("disallowed core must be blocked")
	}
	if !r.Allowed("acme", netengine.RouteDecision{Direct: true}) {
		t.Fatal("DIRECT must be allowed")
	}
	r.Charge("acme", 100)
	if r.Allowed("acme", netengine.RouteDecision{CoreID: contracts.CoreMihomo}) {
		t.Fatal("quota must be enforced")
	}
	if r.Remove("default") {
		t.Fatal("default tenant must be protected")
	}
	if !r.Remove("acme") {
		t.Fatal("tenant removal failed")
	}
	if _, err := r.ResolveTenant(context.Background(), "acme"); err == nil {
		t.Fatal("removed tenant must not resolve")
	}
}
