package nodeapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// A handshake failure used to collapse into "certificate mismatch, regenerate
// the plan". That advice is wrong for the most common real failure: another
// product (Remnawave remnanode) already owns port 2222.
func TestClassifyDistinguishesTLSFailures(t *testing.T) {
	cases := []struct {
		name   string
		err    string
		reason string
		msgHas string
	}{
		{
			name:   "foreign CA",
			err:    "tls: failed to verify certificate: x509: certificate signed by unknown authority",
			reason: ReasonForeignCA,
			msgHas: "不是本面板签发",
		},
		{
			name:   "expired",
			err:    "tls: failed to verify certificate: x509: certificate has expired or is not yet valid",
			reason: ReasonCertExpired,
			msgHas: "过期",
		},
		{
			name:   "another node on this address",
			err:    `节点证书 CN="vps-main" 与期望的 "01" 不一致：确认该地址指向的是这台节点`,
			reason: ReasonWrongNode,
			msgHas: "另一台节点",
		},
		{
			name:   "server rejected our client cert",
			err:    "remote error: tls: certificate required",
			reason: ReasonClientRejected,
			msgHas: "拒绝了面板",
		},
		{
			name:   "generic tls",
			err:    "tls: no cipher suite supported by both client and server",
			reason: ReasonOtherTLS,
			msgHas: "握手失败",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := classify(errors.New(tc.err))
			if e.Kind != KindTLS {
				t.Fatalf("kind = %q, want tls", e.Kind)
			}
			if e.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", e.Reason, tc.reason)
			}
			if !strings.Contains(e.Msg, tc.msgHas) {
				t.Fatalf("message %q does not contain %q", e.Msg, tc.msgHas)
			}
		})
	}
}

func TestClassifyUnreachable(t *testing.T) {
	e := classify(errors.New("dial tcp 50.118.185.194:2244: connect: connection refused"))
	if e.Kind != KindUnreachable {
		t.Fatalf("kind = %q, want unreachable", e.Kind)
	}
	if !strings.Contains(e.Msg, "ngg-node.service") {
		t.Fatalf("message = %q, want an actionable hint", e.Msg)
	}
}

// InspectPeer must reveal a foreign certificate even though we deliberately
// do not trust it — this is the text the operator sees next to "换端口".
func TestInspectPeerReportsForeignCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	got := InspectPeer(context.Background(), "127.0.0.1", port)
	if !strings.Contains(got, "CN=") || !strings.Contains(got, "签发者=") {
		t.Fatalf("InspectPeer = %q, want CN / issuer details", got)
	}
	if bad := InspectPeer(context.Background(), "127.0.0.1", 1); bad != "" {
		t.Fatalf("closed port should produce no diagnosis, got %q", bad)
	}
}
