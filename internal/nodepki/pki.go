// Package nodepki is the certificate authority behind the panel ↔ node
// control channel, modelled on the mTLS scheme Remnawave uses for its node
// API (remnawave/node · src/main.ts: requestCert + rejectUnauthorized, TLS 1.3
// minimum, per-node certificate issued by the panel).
//
// Why certificates instead of the SECRET_KEY in the docker-compose docs: the
// node API listens on a port the whole internet can reach (NODE_PORT, 2244).
// A shared secret in a URL query/header is replayable by anyone who reads one
// packet, and it is the same string for every node. Certificates make each
// node's identity distinct, keep the panel's credential off the wire, and let
// both sides refuse a stranger before a single byte of JSON is exchanged.
//
// Roles:
//
//	CA            generated once per panel, signs every leaf below
//	panel client  CN=ngg-panel, presented by the panel when it dials a node
//	node server   CN=<node id>, SAN=<node address>, presented by ngg-node
//
// A node certificate carries both ServerAuth and ClientAuth so the node can
// call its OWN API during the deploy self-test. The node service only accepts
// a client whose CN is "ngg-panel" or its own node id, so one node cannot use
// that to drive another node.
package nodepki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Names and lifetimes. The leaf lifetime is deliberately shorter than the CA's
// so a node certificate can be re-minted long before the CA expires.
const (
	// PanelCN is the client identity the node API accepts from the panel.
	PanelCN = "ngg-panel"
	// CACN is the CA's subject.
	CACN = "NGG Node CA"

	caLifetime   = 10 * 365 * 24 * time.Hour
	leafLifetime = 5 * 365 * 24 * time.Hour
)

// ErrNoCert means the CA material is missing or unreadable.
var ErrNoCert = errors.New("节点证书材料不可用")

// CA is a loaded or freshly generated certificate authority.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

// NewCA generates a self-signed ECDSA P-256 CA.
func NewCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成 CA 私钥失败: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: CACN, Organization: []string{"NGG"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("生成 CA 证书失败: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{cert: cert, key: key, pem: pemBlock("CERTIFICATE", der)}, nil
}

// LoadCA parses PEM-encoded CA material. Both blocks are required: the panel
// must be able to sign new node certificates after a restart.
func LoadCA(certPEM, keyPEM []byte) (*CA, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%w: CA 证书不是 PEM", ErrNoCert)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoCert, err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("%w: CA 私钥不是 PEM", ErrNoCert)
	}
	key, err := parseECKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoCert, err)
	}
	return &CA{cert: cert, key: key, pem: certBlock.Bytes}, nil
}

// CAPEM is the certificate clients and servers validate against.
func (c *CA) CAPEM() []byte { return append([]byte{}, encodeCert(c.cert.Raw)...) }

// KeyPEM is the CA private key (panel only, 0600 on disk).
func (c *CA) KeyPEM() ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(c.key)
	if err != nil {
		return nil, err
	}
	return pemBlock("PRIVATE KEY", der), nil
}

// NotAfter is the CA expiry, surfaced in the console so an expiring trust root
// is visible instead of failing silently years later.
func (c *CA) NotAfter() time.Time { return c.cert.NotAfter }

// Leaf is one issued certificate.
type Leaf struct {
	CertPEM  []byte
	KeyPEM   []byte
	NotAfter time.Time
	Serial   string
}

// IssueNode mints a certificate for one node. CN is the node id (the panel
// pins it when dialling), SAN is the address the panel will connect to.
//
// The address may be an IPv4 / IPv6 literal or a DNS name; both forms are
// encoded so the operator can address the node either way without re-issuing.
func (c *CA) IssueNode(nodeID, address string) (Leaf, error) {
	nodeID = strings.TrimSpace(nodeID)
	address = strings.TrimSpace(address)
	if nodeID == "" || address == "" {
		return Leaf{}, fmt.Errorf("%w: 签发节点证书需要节点 ID 与地址", ErrNoCert)
	}
	tpl := &x509.Certificate{
		Subject:   pkix.Name{CommonName: nodeID, Organization: []string{"NGG Node"}},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(leafLifetime),
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		// Both usages: server for the panel dialling in, client for the node's
		// own self-test during deploy (the service still checks the CN).
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	if ip := net.ParseIP(address); ip != nil {
		tpl.IPAddresses = []net.IP{ip}
	} else {
		tpl.DNSNames = []string{address}
	}
	// Loopback is always in the SANs: the deploy script verifies the service it
	// just installed by calling https://127.0.0.1:<port>/node/xray/healthcheck,
	// and that check must exercise real hostname verification rather than -k.
	tpl.IPAddresses = append(tpl.IPAddresses, net.IPv4(127, 0, 0, 1), net.IPv6loopback)
	return c.issue(tpl)
}

// IssueClient mints the panel's client certificate.
func (c *CA) IssueClient() (Leaf, error) {
	tpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: PanelCN, Organization: []string{"NGG Panel"}},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(leafLifetime),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	return c.issue(tpl)
}

func (c *CA) issue(tpl *x509.Certificate) (Leaf, error) {
	serial, err := randomSerial()
	if err != nil {
		return Leaf{}, err
	}
	tpl.SerialNumber = serial
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Leaf{}, fmt.Errorf("生成节点密钥失败: %w", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return Leaf{}, fmt.Errorf("签发证书失败: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Leaf{}, err
	}
	return Leaf{
		CertPEM:  pemBlock("CERTIFICATE", der),
		KeyPEM:   pemBlock("PRIVATE KEY", keyDER),
		NotAfter: tpl.NotAfter,
		Serial:   serial.Text(16),
	}, nil
}

/* ------------------------------------------------------------------ *
 * 两端 TLS 配置
 * ------------------------------------------------------------------ */

// ServerConfig builds the node service's TLS configuration: every client must
// present a certificate signed by caPEM, and its CN must be one of allowCN.
//
// allowCN is the second lock. Certificates alone would let any node in the
// fleet drive any other node's API (they are all signed by the same CA); the
// CN list makes the trust directional — only the panel may push config.
func ServerConfig(certPEM, keyPEM, caPEM []byte, allowCN ...string) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("节点证书与私钥不匹配: %w", err)
	}
	pool, err := poolFrom(caPEM)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(allowCN))
	for _, cn := range allowCN {
		if cn = strings.TrimSpace(cn); cn != "" {
			allowed[cn] = true
		}
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("客户端未提供证书")
			}
			cn := cs.PeerCertificates[0].Subject.CommonName
			if !allowed[cn] {
				return fmt.Errorf("客户端证书 CN=%q 不被接受", cn)
			}
			return nil
		},
	}, nil
}

// ClientConfig builds the panel's dialling configuration: the node certificate
// must chain to caPEM and its CN must equal expectCN.
//
// Pinning the CN is what catches "wrong node" — two hosts whose addresses were
// swapped in the registry would otherwise both answer 200 and the panel would
// happily push one node's config onto the other.
func ClientConfig(caPEM, certPEM, keyPEM []byte, serverName, expectCN string) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("面板客户端证书与私钥不匹配: %w", err)
	}
	pool, err := poolFrom(caPEM)
	if err != nil {
		return nil, err
	}
	want := strings.TrimSpace(expectCN)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("节点未提供证书")
			}
			cn := cs.PeerCertificates[0].Subject.CommonName
			if want != "" && cn != want {
				return fmt.Errorf("节点证书 CN=%q 与期望的 %q 不一致：确认该地址指向的是这台节点", cn, want)
			}
			return nil
		},
	}, nil
}

func poolFrom(caPEM []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("%w: CA 证书无法加入信任池", ErrNoCert)
	}
	return pool, nil
}

/* ------------------------------------------------------------------ *
 * 面板侧持久化
 * ------------------------------------------------------------------ */

// Store keeps the panel's CA and client certificate under <data>/nodepki.
//
// The CA private key never leaves this directory: it is the one credential
// that can mint a node identity. Files are 0600, the directory 0700.
type Store struct {
	mu     sync.Mutex
	dir    string
	ca     *CA
	client Leaf
}

// Open loads the PKI material, creating the CA and the panel client
// certificate on first use. A corrupt file is an error, not a silent
// regeneration: re-creating the CA would invalidate every deployed node.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建节点证书目录失败: %w", err)
	}
	caCert, err := readOrEmpty(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return nil, err
	}
	caKey, err := readOrEmpty(filepath.Join(dir, "ca.key"))
	if err != nil {
		return nil, err
	}

	switch {
	case len(caCert) == 0 && len(caKey) == 0:
		ca, cerr := NewCA()
		if cerr != nil {
			return nil, cerr
		}
		keyPEM, kerr := ca.KeyPEM()
		if kerr != nil {
			return nil, kerr
		}
		if werr := writeSecret(filepath.Join(dir, "ca.crt"), ca.CAPEM()); werr != nil {
			return nil, werr
		}
		if werr := writeSecret(filepath.Join(dir, "ca.key"), keyPEM); werr != nil {
			return nil, werr
		}
		s.ca = ca
	case len(caCert) == 0 || len(caKey) == 0:
		return nil, fmt.Errorf("%w: %s 下的 CA 证书或私钥缺失（删掉该目录会重新生成，但已部署的节点需要重新部署）", ErrNoCert, dir)
	default:
		ca, cerr := LoadCA(caCert, caKey)
		if cerr != nil {
			return nil, cerr
		}
		s.ca = ca
	}

	clientCert, err := readOrEmpty(filepath.Join(dir, "panel.crt"))
	if err != nil {
		return nil, err
	}
	clientKey, err := readOrEmpty(filepath.Join(dir, "panel.key"))
	if err != nil {
		return nil, err
	}
	if len(clientCert) == 0 || len(clientKey) == 0 {
		leaf, ierr := s.ca.IssueClient()
		if ierr != nil {
			return nil, ierr
		}
		if werr := writeSecret(filepath.Join(dir, "panel.crt"), leaf.CertPEM); werr != nil {
			return nil, werr
		}
		if werr := writeSecret(filepath.Join(dir, "panel.key"), leaf.KeyPEM); werr != nil {
			return nil, werr
		}
		s.client = leaf
	} else {
		s.client = Leaf{CertPEM: clientCert, KeyPEM: clientKey}
	}
	return s, nil
}

// Dir is where the material lives.
func (s *Store) Dir() string { return s.dir }

// CAPEM returns the trust root the nodes are configured with.
func (s *Store) CAPEM() []byte { return s.ca.CAPEM() }

// CAExpiry exposes the trust root's NotAfter.
func (s *Store) CAExpiry() time.Time { return s.ca.NotAfter() }

// ClientMaterial returns the panel's client certificate for dialling nodes.
func (s *Store) ClientMaterial() (certPEM, keyPEM []byte) {
	return append([]byte{}, s.client.CertPEM...), append([]byte{}, s.client.KeyPEM...)
}

// IssueNode mints a node certificate. The result is embedded in the deploy
// script and never persisted separately: the script itself is the record.
func (s *Store) IssueNode(nodeID, address string) (Leaf, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ca.IssueNode(nodeID, address)
}

/* ------------------------------------------------------------------ *
 * 小工具
 * ------------------------------------------------------------------ */

func pemBlock(kind string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
}

func encodeCert(der []byte) []byte { return pemBlock("CERTIFICATE", der) }

func parseECKey(der []byte) (*ecdsa.PrivateKey, error) {
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if ec, ok := k.(*ecdsa.PrivateKey); ok {
			return ec, nil
		}
		return nil, errors.New("私钥不是 ECDSA")
	}
	if k, err := x509.ParseECPrivateKey(der); err == nil {
		return k, nil
	}
	return nil, errors.New("无法解析 ECDSA 私钥")
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("生成证书序列号失败: %w", err)
	}
	return n.Add(n, big.NewInt(1)), nil
}

func readOrEmpty(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	return b, nil
}

func writeSecret(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	return os.Chmod(path, 0o600)
}
