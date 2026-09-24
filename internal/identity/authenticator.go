package identity

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"studyflow/internal/response"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const ContextUserIDKey = "auth_user_id"

var ErrInvalidToken = errors.New("invalid access token")

type Principal struct {
	UserID    string
	SessionID string
	// ExpiresAt is the expiry declared by the token, already validated.
	ExpiresAt time.Time
}

type jwk struct {
	KTY string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	KID string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

type claims struct {
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

type Authenticator struct {
	url      string
	issuer   string
	audience string
	ttl      time.Duration
	client   *http.Client
	now      func() time.Time

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

func NewAuthenticator(url, issuer, audience string, ttl time.Duration) *Authenticator {
	return &Authenticator{
		url: url, issuer: issuer, audience: audience, ttl: ttl,
		client: &http.Client{Timeout: 5 * time.Second}, now: time.Now,
		keys: make(map[string]*rsa.PublicKey),
	}
}

func (a *Authenticator) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := bearer(c.GetHeader("Authorization"))
		if !ok {
			response.FailUnauthorized(c, "未登录或登录已过期")
			c.Abort()
			return
		}
		principal, err := a.Parse(c.Request.Context(), raw)
		if err != nil {
			response.FailUnauthorized(c, "未登录或登录已过期")
			c.Abort()
			return
		}
		c.Set(ContextUserIDKey, principal.UserID)
		c.Next()
	}
}

func (a *Authenticator) Parse(ctx context.Context, raw string) (Principal, error) {
	parsed, err := jwt.ParseWithClaims(raw, &claims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodRS256 {
			return nil, ErrInvalidToken
		}
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, ErrInvalidToken
		}
		return a.key(ctx, kid)
	}, jwt.WithIssuer(a.issuer), jwt.WithAudience(a.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(30*time.Second))
	if err != nil || !parsed.Valid {
		return Principal{}, ErrInvalidToken
	}
	values, ok := parsed.Claims.(*claims)
	if !ok || values.Subject == "" || values.SessionID == "" {
		return Principal{}, ErrInvalidToken
	}
	if _, err := uuid.Parse(values.Subject); err != nil {
		return Principal{}, ErrInvalidToken
	}
	if _, err := uuid.Parse(values.SessionID); err != nil {
		return Principal{}, ErrInvalidToken
	}
	// ExpiresAt is the expiry the token declared, already enforced above. Callers
	// that hand the principal to another layer need it so that layer can apply its
	// own freshness check instead of trusting an unset value.
	principal := Principal{UserID: values.Subject, SessionID: values.SessionID}
	if values.ExpiresAt != nil {
		principal.ExpiresAt = values.ExpiresAt.Time
	}
	return principal, nil
}

func (a *Authenticator) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.mu.RLock()
	key := a.keys[kid]
	fresh := a.now().Sub(a.fetchedAt) < a.ttl
	a.mu.RUnlock()
	if key != nil && fresh {
		return key, nil
	}
	if err := a.refresh(ctx); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	key = a.keys[kid]
	if key == nil {
		return nil, ErrInvalidToken
	}
	return key, nil
}

func (a *Authenticator) refresh(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url, nil)
	if err != nil {
		return err
	}
	result, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		return ErrInvalidToken
	}
	var document jwks
	if err := json.NewDecoder(result.Body).Decode(&document); err != nil {
		return err
	}
	keys := make(map[string]*rsa.PublicKey, len(document.Keys))
	for _, item := range document.Keys {
		if item.KTY != "RSA" || item.Alg != "RS256" || item.KID == "" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(item.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(item.E)
		if err != nil || len(eBytes) == 0 || len(eBytes) > 4 {
			continue
		}
		exponent := 0
		for _, value := range eBytes {
			exponent = exponent<<8 + int(value)
		}
		if exponent < 3 {
			continue
		}
		keys[item.KID] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exponent}
	}
	if len(keys) == 0 {
		return ErrInvalidToken
	}
	a.keys = keys
	a.fetchedAt = a.now()
	return nil
}

func bearer(value string) (string, bool) {
	parts := strings.Fields(value)
	returnValue := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		returnValue = parts[1]
	}
	return returnValue, returnValue != ""
}

func UserID(c *gin.Context) (string, bool) {
	value, exists := c.Get(ContextUserIDKey)
	userID, ok := value.(string)
	return userID, exists && ok && userID != ""
}
