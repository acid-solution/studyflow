package identity

import (
	"crypto/rand"
	"crypto/rsa"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestDelegationHasSeparateAudienceAndRestrictedLifetime(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := time.Now().UTC().Truncate(time.Second)
	a := NewAuthenticator("", "shared-auth", "studyflow", time.Hour)
	a.keys["key"] = &key.PublicKey
	a.fetchedAt = now
	a.now = func() time.Time { return now }
	resource := "http://127.0.0.1:8081/mcp"
	c := Delegation{SessionID: uuid.NewString(), Delegated: true, Actor: map[string]string{"sub": "jobpilot"}, Scope: "learning_route:import learning_route:read", ImportKey: "import-123", ContentDigest: strings.Repeat("a", 64), RegisteredClaims: jwt.RegisteredClaims{Issuer: "shared-auth", Subject: uuid.NewString(), Audience: jwt.ClaimStrings{resource}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))}}
	sign := func(c Delegation) string {
		v := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
		v.Header["kid"] = "key"
		raw, e := v.SignedString(key)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	raw := sign(c)
	if _, err := a.ParseDelegation(t.Context(), raw, resource); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Parse(t.Context(), raw); err == nil {
		t.Fatal("delegation accepted by REST")
	}
	for _, change := range []func(*Delegation){func(c *Delegation) { c.Scope = "*" }, func(c *Delegation) { c.ExpiresAt = jwt.NewNumericDate(now.Add(3 * time.Minute)) }, func(c *Delegation) { c.Audience = jwt.ClaimStrings{"studyflow"} }, func(c *Delegation) { c.Actor["sub"] = "attacker" }} {
		copy := c
		copy.Actor = map[string]string{"sub": "jobpilot"}
		change(&copy)
		if _, err := a.ParseDelegation(t.Context(), sign(copy), resource); err == nil {
			t.Fatal("invalid delegation accepted")
		}
	}
}
