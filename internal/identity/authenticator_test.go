package identity

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestAuthenticatorValidatesIssuerAudienceAndSignature(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kid := "test-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exponent := big.NewInt(int64(privateKey.PublicKey.E)).Bytes()
		_ = json.NewEncoder(w).Encode(jwks{Keys: []jwk{{KTY: "RSA", Use: "sig", Alg: "RS256", KID: kid, N: base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()), E: base64.RawURLEncoding.EncodeToString(exponent)}}})
	}))
	defer server.Close()
	authenticator := NewAuthenticator(server.URL, "shared-auth", "studyflow", time.Minute)
	userID, sessionID := uuid.NewString(), uuid.NewString()

	sign := func(audience string, key *rsa.PrivateKey) string {
		now := time.Now().UTC()
		value := jwt.NewWithClaims(jwt.SigningMethodRS256, claims{SessionID: sessionID, RegisteredClaims: jwt.RegisteredClaims{Issuer: "shared-auth", Subject: userID, Audience: jwt.ClaimStrings{audience}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))}})
		value.Header["kid"] = kid
		raw, err := value.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	principal, err := authenticator.Parse(t.Context(), sign("studyflow", privateKey))
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if principal.UserID != userID || principal.SessionID != sessionID {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	if _, err := authenticator.Parse(t.Context(), sign("jobpilot", privateKey)); err == nil {
		t.Fatal("jobpilot audience was accepted")
	}
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err := authenticator.Parse(t.Context(), sign("studyflow", otherKey)); err == nil {
		t.Fatal("tampered signature was accepted")
	}
}
