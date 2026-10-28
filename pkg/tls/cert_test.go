package tls

import (
	"crypto/x509"
	"testing"
	"time"
)

func TestGenerateSelfSignedCert(t *testing.T) {
	cert, err := GenerateSelfSignedCert(30)
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Fatalf("no certificate bytes returned")
	}

	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse generated certificate: %v", err)
	}

	if parsed.NotBefore.After(time.Now()) {
		t.Errorf("certificate NotBefore is in the future")
	}

	expectedExpiry := time.Now().Add(29 * 24 * time.Hour)
	if parsed.NotAfter.Before(expectedExpiry) {
		t.Errorf("certificate expiry is sooner than expected: %v", parsed.NotAfter)
	}
}
