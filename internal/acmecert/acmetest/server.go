// Package acmetest is a fake ACME CA (RFC 8555) for tests. It never touches
// the network beyond loopback. JWS signatures are not verified, but the
// TLS-ALPN-01 check is real: it dials the node's listener with ALPN
// acme-tls/1 and the domain as SNI, and compares the acmeIdentifier extension
// with sha256 of the key authorisation.
package acmetest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
)

var oidACMEIdentifier = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 31}

// Server is a fake ACME CA.
type Server struct {
	// DialAddr maps a domain to the host:port of the listener that must
	// answer the TLS-ALPN-01 probe. Required.
	DialAddr func(domain string) string
	// Lifetime is the validity of issued certificates. Default 90 days.
	Lifetime time.Duration

	srv  *httptest.Server
	ca   *x509.Certificate
	caK  *ecdsa.PrivateKey
	hits atomic.Int64

	mu       sync.Mutex
	fail     bool
	tamper   Tamper
	nextID   int
	accounts map[string]string // account URL -> JWK thumbprint
	authzs   map[string]*authz
	orders   map[string]*order
	certs    map[string][]byte
	// Validated records each domain whose challenge passed, in order.
	validated []string
	// Failures records why each validation failed.
	failures []string
	issued   []*x509.Certificate
}

type authz struct {
	id, domain, token, status, thumb string
	problem                          string
}

type order struct {
	id      string
	domains []string
	authzs  []string
	status  string
	certURL string
}

// New starts the server. dial maps a domain to the listener address.
func New(t testing.TB, dial func(domain string) string) *Server {
	t.Helper()
	s := &Server{
		DialAddr: dial,
		Lifetime: 90 * 24 * time.Hour,
		accounts: map[string]string{},
		authzs:   map[string]*authz{},
		orders:   map[string]*order{},
		certs:    map[string][]byte{},
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Fake ACME Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	s.ca, _ = x509.ParseCertificate(der)
	s.caK = k
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

// DirectoryURL is the https URL to configure as the directory.
func (s *Server) DirectoryURL() string { return s.srv.URL + "/directory" }

// Client is an HTTP client that trusts the fake CA's TLS certificate. Tests
// hand it to the manager; it is never reachable from configuration.
func (s *Server) Client() *http.Client { return s.srv.Client() }

// Hits is the number of requests the server has received.
func (s *Server) Hits() int { return int(s.hits.Load()) }

// SetFailing makes every validation fail (the probe is answered "invalid").
func (s *Server) SetFailing(on bool) {
	s.mu.Lock()
	s.fail = on
	s.mu.Unlock()
}

// Validated returns the domains whose TLS-ALPN-01 check passed.
func (s *Server) Validated() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.validated)
}

// Failures returns the reasons validations failed.
func (s *Server) Failures() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.failures)
}

// Tamper is a defect the fake CA puts into the leaves it issues, so tests can
// check that the client refuses a certificate a misbehaving CA hands back.
type Tamper int

const (
	// TamperNone issues the names and key of the CSR, valid for Lifetime.
	TamperNone Tamper = iota
	// TamperDropName leaves the last requested name out of the leaf.
	TamperDropName
	// TamperExtraName adds a name nobody requested to the leaf.
	TamperExtraName
	// TamperOtherKey certifies a key the CA made instead of the CSR's.
	TamperOtherKey
	// TamperExpired issues a leaf whose validity has already ended.
	TamperExpired
)

// SetTamper selects the defect put into every leaf issued from now on.
func (s *Server) SetTamper(t Tamper) {
	s.mu.Lock()
	s.tamper = t
	s.mu.Unlock()
}

// Issued returns the certificates signed so far.
func (s *Server) Issued() []*x509.Certificate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.issued)
}

type jws struct {
	Protected string `json:"protected"`
	Payload   string `json:"payload"`
}

type protectedHeader struct {
	JWK *struct {
		Crv, X, Y string
	} `json:"jwk"`
	KID string `json:"kid"`
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.hits.Add(1)
	w.Header().Set("Replay-Nonce", randHex(12))
	base := s.srv.URL
	path := r.URL.Path
	switch path {
	case "/directory":
		writeJSON(w, http.StatusOK, nil, map[string]any{
			"newNonce":   base + "/newNonce",
			"newAccount": base + "/newAccount",
			"newOrder":   base + "/newOrder",
			"revokeCert": base + "/revokeCert",
			"keyChange":  base + "/keyChange",
		})
		return
	case "/newNonce":
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var env jws
	if json.Unmarshal(body, &env) != nil {
		problem(w, http.StatusBadRequest, "malformed", "bad JWS")
		return
	}
	hdrRaw, _ := base64.RawURLEncoding.DecodeString(env.Protected)
	var hdr protectedHeader
	_ = json.Unmarshal(hdrRaw, &hdr)
	payload, _ := base64.RawURLEncoding.DecodeString(env.Payload)

	switch {
	case path == "/newAccount":
		s.newAccount(w, hdr, payload)
	case path == "/newOrder":
		s.newOrder(w, payload)
	case strings.HasPrefix(path, "/authz/"):
		s.getAuthz(w, strings.TrimPrefix(path, "/authz/"))
	case strings.HasPrefix(path, "/chal/"):
		s.startChallenge(w, strings.TrimPrefix(path, "/chal/"), hdr)
	case strings.HasPrefix(path, "/finalize/"):
		s.finalize(w, strings.TrimPrefix(path, "/finalize/"), payload)
	case strings.HasPrefix(path, "/order/"):
		s.getOrder(w, strings.TrimPrefix(path, "/order/"))
	case strings.HasPrefix(path, "/cert/"):
		s.getCert(w, strings.TrimPrefix(path, "/cert/"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) newAccount(w http.ResponseWriter, hdr protectedHeader, _ []byte) {
	if hdr.JWK == nil {
		problem(w, http.StatusBadRequest, "malformed", "no jwk")
		return
	}
	x, _ := base64.RawURLEncoding.DecodeString(hdr.JWK.X)
	y, _ := base64.RawURLEncoding.DecodeString(hdr.JWK.Y)
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
	if err != nil {
		problem(w, http.StatusBadRequest, "malformed", "bad jwk")
		return
	}
	thumb, err := acme.JWKThumbprint(pub)
	if err != nil {
		problem(w, http.StatusBadRequest, "malformed", "bad jwk")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for url, th := range s.accounts {
		if th == thumb {
			writeJSON(w, http.StatusOK, map[string]string{"Location": url}, map[string]any{"status": "valid"})
			return
		}
	}
	s.nextID++
	url := s.srv.URL + "/acct/" + strconv.Itoa(s.nextID)
	s.accounts[url] = thumb
	writeJSON(w, http.StatusCreated, map[string]string{"Location": url}, map[string]any{"status": "valid"})
}

func (s *Server) newOrder(w http.ResponseWriter, payload []byte) {
	var req struct {
		Identifiers []struct{ Type, Value string } `json:"identifiers"`
	}
	if json.Unmarshal(payload, &req) != nil || len(req.Identifiers) == 0 {
		problem(w, http.StatusBadRequest, "malformed", "bad order")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	o := &order{id: strconv.Itoa(s.nextID), status: "pending"}
	var thumb string
	for _, th := range s.accounts {
		thumb = th // the tests use a single account
	}
	for _, id := range req.Identifiers {
		s.nextID++
		a := &authz{id: strconv.Itoa(s.nextID), domain: id.Value, token: randHex(16), status: "pending", thumb: thumb}
		s.authzs[a.id] = a
		o.authzs = append(o.authzs, a.id)
		o.domains = append(o.domains, id.Value)
	}
	s.orders[o.id] = o
	writeJSON(w, http.StatusCreated, map[string]string{"Location": s.srv.URL + "/order/" + o.id}, s.orderJSON(o))
}

func (s *Server) orderJSON(o *order) map[string]any {
	// The caller holds s.mu.
	var urls []string
	var idents []map[string]string
	for _, id := range o.authzs {
		urls = append(urls, s.srv.URL+"/authz/"+id)
	}
	for _, d := range o.domains {
		idents = append(idents, map[string]string{"type": "dns", "value": d})
	}
	status := o.status
	if status == "pending" {
		all, bad := true, false
		for _, id := range o.authzs {
			switch s.authzs[id].status {
			case "valid":
			case "invalid":
				bad = true
				all = false
			default:
				all = false
			}
		}
		if bad {
			status = "invalid"
		} else if all {
			status = "ready"
		}
	}
	out := map[string]any{
		"status":         status,
		"identifiers":    idents,
		"authorizations": urls,
		"finalize":       s.srv.URL + "/finalize/" + o.id,
	}
	if o.certURL != "" {
		out["certificate"] = o.certURL
	}
	return out
}

func (s *Server) getOrder(w http.ResponseWriter, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orders[id]
	if o == nil {
		problem(w, http.StatusNotFound, "malformed", "no such order")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"Location": s.srv.URL + "/order/" + o.id}, s.orderJSON(o))
}

func (s *Server) getAuthz(w http.ResponseWriter, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.authzs[id]
	if a == nil {
		problem(w, http.StatusNotFound, "malformed", "no such authz")
		return
	}
	chal := map[string]any{
		"type":   "tls-alpn-01",
		"url":    s.srv.URL + "/chal/" + a.id,
		"token":  a.token,
		"status": map[string]string{"valid": "valid", "invalid": "invalid"}[a.status],
	}
	if a.status == "pending" {
		chal["status"] = "pending"
	}
	if a.status == "invalid" {
		chal["error"] = map[string]any{"type": "urn:ietf:params:acme:error:unauthorized", "detail": a.problem, "status": 403}
	}
	writeJSON(w, http.StatusOK, nil, map[string]any{
		"status":     a.status,
		"identifier": map[string]string{"type": "dns", "value": a.domain},
		"challenges": []any{chal},
	})
}

func (s *Server) startChallenge(w http.ResponseWriter, id string, hdr protectedHeader) {
	s.mu.Lock()
	a := s.authzs[id]
	s.mu.Unlock()
	if a == nil {
		problem(w, http.StatusNotFound, "malformed", "no such challenge")
		return
	}
	go s.validate(a)
	writeJSON(w, http.StatusOK, nil, map[string]any{
		"type": "tls-alpn-01", "url": s.srv.URL + "/chal/" + a.id, "token": a.token, "status": "processing",
	})
	_ = hdr
}

// validate performs the real TLS-ALPN-01 check.
func (s *Server) validate(a *authz) {
	err := s.probe(a)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		a.status, a.problem = "invalid", err.Error()
		s.failures = append(s.failures, a.domain+": "+err.Error())
		return
	}
	a.status = "valid"
	s.validated = append(s.validated, a.domain)
}

func (s *Server) probe(a *authz) error {
	s.mu.Lock()
	failing := s.fail
	s.mu.Unlock()
	if failing {
		return fmt.Errorf("validation refused by the test CA")
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", s.DialAddr(a.domain), &tls.Config{
		ServerName:         a.domain,
		NextProtos:         []string{acme.ALPNProto},
		InsecureSkipVerify: true, // #nosec G402 -- the challenge certificate is self-signed by design
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()
	st := conn.ConnectionState()
	if st.NegotiatedProtocol != acme.ALPNProto {
		return fmt.Errorf("acme-tls/1 was not negotiated")
	}
	if len(st.PeerCertificates) == 0 {
		return fmt.Errorf("no certificate")
	}
	leaf := st.PeerCertificates[0]
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != a.domain {
		return fmt.Errorf("certificate names do not match the domain")
	}
	want := sha256.Sum256([]byte(a.token + "." + a.thumb))
	for _, ext := range leaf.Extensions {
		if !ext.Id.Equal(oidACMEIdentifier) {
			continue
		}
		if !ext.Critical {
			return fmt.Errorf("acmeIdentifier extension is not critical")
		}
		var got []byte
		if _, err := asn1.Unmarshal(ext.Value, &got); err != nil {
			return fmt.Errorf("acmeIdentifier extension does not parse")
		}
		if hex.EncodeToString(got) != hex.EncodeToString(want[:]) {
			return fmt.Errorf("acmeIdentifier does not match the key authorisation")
		}
		return nil
	}
	return fmt.Errorf("no acmeIdentifier extension")
}

func (s *Server) finalize(w http.ResponseWriter, id string, payload []byte) {
	var req struct {
		CSR string `json:"csr"`
	}
	if json.Unmarshal(payload, &req) != nil {
		problem(w, http.StatusBadRequest, "malformed", "bad finalize")
		return
	}
	der, _ := base64.RawURLEncoding.DecodeString(req.CSR)
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil || csr.CheckSignature() != nil {
		problem(w, http.StatusBadRequest, "badCSR", "bad CSR")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orders[id]
	if o == nil || s.orderJSON(o)["status"] != "ready" {
		problem(w, http.StatusForbidden, "orderNotReady", "order not ready")
		return
	}
	if !slices.Equal(sorted(csr.DNSNames), sorted(o.domains)) {
		problem(w, http.StatusBadRequest, "badCSR", "CSR names differ from the order")
		return
	}
	s.nextID++
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(int64(1000 + s.nextID)),
		Subject:      pkix.Name{CommonName: csr.DNSNames[0]},
		DNSNames:     csr.DNSNames,
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(s.Lifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	pub := csr.PublicKey
	switch s.tamper {
	case TamperDropName:
		if len(leafTmpl.DNSNames) > 1 {
			leafTmpl.DNSNames = slices.Clone(leafTmpl.DNSNames[:len(leafTmpl.DNSNames)-1])
		} else {
			leafTmpl.DNSNames = []string{"other.invalid"}
		}
	case TamperExtraName:
		leafTmpl.DNSNames = append(slices.Clone(leafTmpl.DNSNames), "extra.invalid")
	case TamperOtherKey:
		other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			problem(w, http.StatusInternalServerError, "serverInternal", "key generation failed")
			return
		}
		pub = &other.PublicKey
	case TamperExpired:
		leafTmpl.NotBefore = time.Now().Add(-48 * time.Hour)
		leafTmpl.NotAfter = time.Now().Add(-24 * time.Hour)
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, s.ca, pub, s.caK)
	if err != nil {
		problem(w, http.StatusInternalServerError, "serverInternal", "signing failed")
		return
	}
	leaf, _ := x509.ParseCertificate(leafDER)
	s.issued = append(s.issued, leaf)
	chain := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.ca.Raw})...)
	certID := strconv.Itoa(s.nextID)
	s.certs[certID] = chain
	o.status = "valid"
	o.certURL = s.srv.URL + "/cert/" + certID
	writeJSON(w, http.StatusOK, map[string]string{"Location": s.srv.URL + "/order/" + o.id}, s.orderJSON(o))
}

func (s *Server) getCert(w http.ResponseWriter, id string) {
	s.mu.Lock()
	chain := s.certs[id]
	s.mu.Unlock()
	if chain == nil {
		problem(w, http.StatusNotFound, "malformed", "no such certificate")
		return
	}
	w.Header().Set("Content-Type", "application/pem-certificate-chain")
	_, _ = w.Write(chain)
}

func sorted(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, code int, hdr map[string]string, v any) {
	w.Header().Set("Content-Type", "application/json")
	for k, val := range hdr {
		w.Header().Set(k, val)
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func problem(w http.ResponseWriter, code int, typ, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "urn:ietf:params:acme:error:" + typ, "detail": detail, "status": code})
}

// Tokens returns every challenge token the server has handed out, so a test
// can assert none reaches a log.
func (s *Server) Tokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, a := range s.authzs {
		out = append(out, a.token)
	}
	return out
}
