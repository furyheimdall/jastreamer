package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func TestRun_externalTLS_AtoBtoA_preserves_durable_state_and_exposes_real_mismatch(t *testing.T) {
	data := t.TempDir()
	pairA := externalPair(t, "a")
	pairB := externalPair(t, "b")
	start := func(pair [2]string) liveServer {
		return startLiveServerConfig(t, serverConfig{
			address: "127.0.0.1:0", dataDirectory: data, catalogRoot: filepath.Join(data, "media"),
			catalogMigrationPath: "../../migrations/001_catalog.sql", playbackMigrationPath: "../../migrations/002_playback.sql",
			playbackExpansionPath: "../../migrations/003_todo12.sql", setupSecret: "integration-setup",
			certificateDNS: []string{"localhost"}, certificateIPs: []net.IP{net.ParseIP("127.0.0.1")}, pairingTTL: 5 * time.Minute,
			tlsCertificatePath: pair[0], tlsPrivateKeyPath: pair[1],
		})
	}

	first := start(pairA)
	_, bootstrapBody := first.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/bootstrap", body: `{"setup_secret":"integration-setup","name":"Admin"}`})
	var credential struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(bootstrapBody, &credential); err != nil || credential.Token == "" {
		t.Fatalf("bootstrap = %s, %v", bootstrapBody, err)
	}
	created, createdBody := first.request(t, liveRequest{method: http.MethodPost, path: "/api/v1/zones", token: credential.Token, body: `{"zone_id":"durable","name":"Durable"}`})
	spkiA := liveSPKI(t, first.url)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create durable zone = %d %s", created.StatusCode, createdBody)
	}
	if err := first.stop(); err != nil {
		t.Fatal(err)
	}
	durable, err := security.LoadOrCreateIdentity(security.IdentityConfig{Directory: filepath.Join(data, "identity"), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}})
	if err != nil {
		t.Fatal(err)
	}

	second := start(pairB)
	spkiB := liveSPKI(t, second.url)
	zonesB, bodyB := second.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/zones", token: credential.Token})
	mismatch := pinnedIdentityRequest(second.url, first.fingerprint)
	if zonesB.StatusCode != http.StatusOK || !strings.Contains(string(bodyB), `"zone_id":"durable"`) || first.fingerprint == second.fingerprint || spkiA == spkiB || mismatch == nil || !strings.Contains(mismatch.Error(), "certificate identity changed") {
		t.Fatalf("B state/mismatch = status:%d body:%s cert:%s/%s spki:%s/%s mismatch:%v", zonesB.StatusCode, bodyB, first.fingerprint, second.fingerprint, spkiA, spkiB, mismatch)
	}
	if err := second.stop(); err != nil {
		t.Fatal(err)
	}

	third := start(pairA)
	zonesA, bodyA := third.request(t, liveRequest{method: http.MethodGet, path: "/api/v1/zones", token: credential.Token})
	if zonesA.StatusCode != http.StatusOK || !strings.Contains(string(bodyA), `"zone_id":"durable"`) || third.fingerprint != first.fingerprint || liveSPKI(t, third.url) != spkiA || pinnedIdentityRequest(third.url, first.fingerprint) != nil {
		t.Fatalf("restored A state = status:%d body:%s cert:%s/%s", zonesA.StatusCode, bodyA, first.fingerprint, third.fingerprint)
	}
	restoredDurable, err := security.LoadOrCreateIdentity(security.IdentityConfig{Directory: filepath.Join(data, "identity"), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}})
	if err != nil || restoredDurable.Fingerprint != durable.Fingerprint || durable.Fingerprint == first.fingerprint || durable.Fingerprint == second.fingerprint {
		t.Fatalf("durable identity changed = %s/%s, %v", durable.Fingerprint, restoredDurable.Fingerprint, err)
	}
	t.Logf("external_tls cert_a=%s cert_b=%s spki_a=%s spki_b=%s durable_zone=durable restored_cert=%s", first.fingerprint, second.fingerprint, spkiA, spkiB, third.fingerprint)
}

func externalPair(t *testing.T, name string) [2]string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), name)
	if _, err := security.LoadOrCreateIdentity(security.IdentityConfig{Directory: directory, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}); err != nil {
		t.Fatal(err)
	}
	return [2]string{filepath.Join(directory, "tls-cert.pem"), filepath.Join(directory, "tls-key.pem")}
}

func liveSPKI(t *testing.T, origin string) string {
	t.Helper()
	parsed, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := tls.Dial("tcp", parsed.Host, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	certificate := connection.ConnectionState().PeerCertificates[0]
	digest := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(digest[:])
}

func pinnedIdentityRequest(origin, fingerprint string) error {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) != 1 {
			return errors.New("certificate identity changed")
		}
		digest := sha256.Sum256(rawCerts[0])
		if hex.EncodeToString(digest[:]) != fingerprint {
			return errors.New("certificate identity changed")
		}
		return nil
	}}}
	defer transport.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, origin+"/api/v1/identity", nil)
	response, err := transport.RoundTrip(request)
	if err == nil {
		response.Body.Close()
	}
	return err
}
