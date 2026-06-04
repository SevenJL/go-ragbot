// Package auth provides JWT authentication and role-based access control
// using only the Go standard library (HMAC-SHA256).
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Role represents an access level.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// Claims is the JWT payload.
type Claims struct {
	Sub    string `json:"sub"`    // user identifier
	Role   Role   `json:"role"`   // admin | user
	Tenant string `json:"tenant"` // tenant identifier
	Iat    int64  `json:"iat"`    // issued at (unix milliseconds)
	Exp    int64  `json:"exp"`    // expiration (unix milliseconds)
}

// Token represents a signed JWT.
type Token struct {
	Raw    string
	Claims Claims
}

// Issuer creates and verifies JWT tokens.
type Issuer struct {
	secret    []byte
	duration  time.Duration
	blacklist map[string]time.Time // jti → expiry (for revocation)
	mu        sync.RWMutex
}

// NewIssuer creates a JWT issuer with the given signing secret and token TTL.
func NewIssuer(secret string, ttl time.Duration) *Issuer {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Issuer{
		secret:    []byte(secret),
		duration:  ttl,
		blacklist: make(map[string]time.Time),
	}
}

// Issue creates a signed JWT for the given subject and role. jti is a unique
// token ID used for revocation.
func (i *Issuer) Issue(sub string, role Role, jti string) (*Token, error) {
	return i.IssueForTenant(sub, role, sub, jti)
}

// IssueForTenant creates a signed JWT bound to a tenant ID.
func (i *Issuer) IssueForTenant(sub string, role Role, tenant string, jti string) (*Token, error) {
	now := time.Now()
	if tenant == "" {
		tenant = sub
	}
	c := Claims{
		Sub:    sub,
		Role:   role,
		Tenant: tenant,
		Iat:    now.UnixMilli(),
		Exp:    now.Add(i.duration).UnixMilli(),
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, _ := json.Marshal(c)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signingInput := header + "." + payload

	mac := hmac.New(sha256.New, i.secret)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	raw := signingInput + "." + sig
	return &Token{Raw: raw, Claims: c}, nil
}

// Verify parses and validates a JWT string. Returns the claims if valid, or
// an error if expired, tampered, or revoked.
func (i *Issuer) Verify(raw string) (*Claims, error) {
	parts := strings.SplitN(raw, ".", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	signingInput := parts[0] + "." + parts[1]

	// Verify signature.
	mac := hmac.New(sha256.New, i.secret)
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return nil, fmt.Errorf("invalid signature")
	}

	// Decode claims.
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid payload encoding: %w", err)
	}
	var c Claims
	if err := json.Unmarshal(payloadBytes, &c); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	// Check expiration.
	if time.Now().UnixMilli() >= c.Exp {
		return nil, fmt.Errorf("token expired")
	}

	// Check blacklist (revocation via jti derived from sub+iat).
	i.mu.RLock()
	jti := fmt.Sprintf("%s:%d", c.Sub, c.Iat)
	if _, revoked := i.blacklist[jti]; revoked {
		i.mu.RUnlock()
		return nil, fmt.Errorf("token revoked")
	}
	i.mu.RUnlock()

	// Cleanup expired blacklist entries occasionally.
	i.cleanupBlacklist()

	return &c, nil
}

// Revoke invalidates a specific token by its jti (sub:iat format).
func (i *Issuer) Revoke(sub string, iat int64) {
	i.mu.Lock()
	jti := fmt.Sprintf("%s:%d", sub, iat)
	i.blacklist[jti] = time.Now()
	i.mu.Unlock()
}

func (i *Issuer) cleanupBlacklist() {
	i.mu.Lock()
	defer i.mu.Unlock()
	now := time.Now()
	for jti, t := range i.blacklist {
		if now.Sub(t) > i.duration*2 {
			delete(i.blacklist, jti)
		}
	}
}

// HasRole reports whether the claims contain at least the required role.
// Admin includes user privileges.
func HasRole(c *Claims, required Role) bool {
	if c == nil {
		return false
	}
	if c.Role == RoleAdmin {
		return true // admin can do anything
	}
	return c.Role == required
}
