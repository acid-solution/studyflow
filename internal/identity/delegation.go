package identity

import (
	"context"
	"encoding/hex"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"strings"
	"time"
)

type Delegation struct {
	SessionID     string            `json:"sid"`
	Delegated     bool              `json:"delegated"`
	Actor         map[string]string `json:"act"`
	Scope         string            `json:"scope"`
	ImportKey     string            `json:"import_key"`
	ContentDigest string            `json:"content_digest"`
	jwt.RegisteredClaims
}

func (a *Authenticator) ParseDelegation(ctx context.Context, raw, resource string) (*Delegation, error) {
	if resource == "" {
		return nil, ErrInvalidToken
	}
	c := &Delegation{}
	parsed, err := jwt.ParseWithClaims(raw, c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if t.Method != jwt.SigningMethodRS256 || kid == "" {
			return nil, ErrInvalidToken
		}
		return a.key(ctx, kid)
	}, jwt.WithIssuer(a.issuer), jwt.WithAudience(resource), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(a.now))
	if err != nil || !parsed.Valid || !c.Delegated || len(c.Audience) != 1 || c.Audience[0] != resource || c.Actor["sub"] != "jobpilot" || c.IssuedAt == nil || c.ExpiresAt == nil || c.ExpiresAt.Sub(c.IssuedAt.Time) > 2*time.Minute || c.Scope != "learning_route:import learning_route:read" {
		return nil, ErrInvalidToken
	}
	if _, err = uuid.Parse(c.Subject); err != nil {
		return nil, ErrInvalidToken
	}
	if _, err = uuid.Parse(c.SessionID); err != nil {
		return nil, ErrInvalidToken
	}
	digest, err := hex.DecodeString(c.ContentDigest)
	if err != nil || len(digest) != 32 || c.ContentDigest != strings.ToLower(c.ContentDigest) || len(c.ImportKey) < 8 || len(c.ImportKey) > 200 {
		return nil, ErrInvalidToken
	}
	for _, r := range c.ImportKey {
		if r < 33 || r > 126 {
			return nil, ErrInvalidToken
		}
	}
	return c, nil
}
