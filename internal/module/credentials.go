package module

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"strings"

	"github.com/ngg-project/ngg/internal/nodepki"
	"github.com/ngg-project/ngg/internal/pq"
)

// NewNodeCredentials is 规划 1: the operator supplies ONLY address + port.
// Everything else is minted here, atomically, and the private material is
// returned in NodeSecrets exactly once (never persisted, never recoverable).
func NewNodeCredentials(name, address string, port int, pkiDir string) (NodeSpec, NodeSecrets, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return NodeSpec{}, NodeSecrets{}, fmt.Errorf("节点地址不能为空")
	}
	if port < 1 || port > 65535 {
		return NodeSpec{}, NodeSecrets{}, fmt.Errorf("节点端口需在 1..65535，当前 %d", port)
	}
	if net.ParseIP(address) == nil && !strings.Contains(address, ".") {
		return NodeSpec{}, NodeSecrets{}, fmt.Errorf("节点地址需是 IP 或域名：%q", address)
	}

	id, err := randomID("node-")
	if err != nil {
		return NodeSpec{}, NodeSecrets{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = id
	}

	// 1) API password — 32 random bytes, stored only as a SHA-256 digest.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return NodeSpec{}, NodeSecrets{}, fmt.Errorf("生成 API 密码失败: %w", err)
	}
	apiPassword := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(apiPassword))

	// 2) Post-quantum key — reuse the project's hybrid ML-KEM-768 + X25519.
	kp, err := pq.GenerateKeyPair()
	if err != nil {
		return NodeSpec{}, NodeSecrets{}, fmt.Errorf("生成后量子密钥失败: %w", err)
	}
	ek := kp.MLKEM.EncapsulationKey().Bytes()
	xp := kp.X25519.PublicKey().Bytes()
	pqPublic := base64.RawURLEncoding.EncodeToString(append(append([]byte{}, ek...), xp...))
	pqSum := sha256.Sum256([]byte(pqPublic))

	// 3) mTLS certificate — issued by the panel's own CA, CN = node ID.
	pki, err := nodepki.Open(pkiDir)
	if err != nil {
		return NodeSpec{}, NodeSecrets{}, fmt.Errorf("打开节点 CA 失败: %w", err)
	}
	leaf, err := pki.IssueNode(id, address)
	if err != nil {
		return NodeSpec{}, NodeSecrets{}, fmt.Errorf("签发 mTLS 证书失败: %w", err)
	}
	certSum := sha256.Sum256(leaf.CertPEM)

	spec := NodeSpec{
		ID: id, Name: name, Address: address, Port: port,
		APIPasswordHash: hex.EncodeToString(sum[:]),
		PQSuite:         pq.Suite,
		PQPublic:        pqPublic,
		PQFingerprint:   hex.EncodeToString(pqSum[:])[:32],
		MTLSCommonName:  id,
		MTLSFingerprint: hex.EncodeToString(certSum[:])[:32],
		CACertPEM:       string(pki.CAPEM()),
		NodeCertPEM:     string(leaf.CertPEM),
	}

	secrets := NodeSecrets{
		APIPassword:   apiPassword,
		PQPublic:      pqPublic,
		PQFingerprint: spec.PQFingerprint,
		NodeKeyPEM:    string(leaf.KeyPEM),
		NodeCertPEM:   string(leaf.CertPEM),
		CACertPEM:     string(pki.CAPEM()),
		Notice:        "API 密码、后量子公钥、节点私钥仅本次显示，请立即保存；服务端只保留哈希与指纹。",
	}
	return spec, secrets, nil
}

// VerifyAPIPassword checks a presented password in constant time.
func (n NodeSpec) VerifyAPIPassword(candidate string) bool {
	sum := sha256.Sum256([]byte(candidate))
	want, err := hex.DecodeString(n.APIPasswordHash)
	if err != nil || len(want) != len(sum) {
		return false
	}
	var diff byte
	for i := range sum {
		diff |= sum[i] ^ want[i]
	}
	return diff == 0
}

func randomID(prefix string) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 ID 失败: %w", err)
	}
	return prefix + hex.EncodeToString(b), nil
}
