// Package auth authenticates gateway clients with API keys.
//
// Keys are never stored: the keys file holds one "name:sha256hex" line per
// client. Incoming keys are hashed and looked up by hash, so the comparison
// does not leak timing information about the stored secrets.
package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const keyPrefix = "gw_"

// Store maps key hashes to client names.
type Store struct {
	clients map[[sha256.Size]byte]string
}

// GenerateKey returns a new random API key.
func GenerateKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return keyPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashKey returns the hex-encoded SHA-256 of key, as stored in the keys file.
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// ParseKeys reads a keys file. Blank lines and lines starting with # are
// ignored. Client names and hashes must be unique, and at least one key is
// required so a misconfigured gateway never starts open.
func ParseKeys(r io.Reader) (*Store, error) {
	store := &Store{clients: make(map[[sha256.Size]byte]string)}
	names := make(map[string]bool)

	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rawHash, ok := strings.Cut(line, ":")
		if !ok || name == "" {
			return nil, fmt.Errorf("keys line %d: want name:sha256hex", n)
		}
		decoded, err := hex.DecodeString(rawHash)
		if err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("keys line %d: hash must be %d hex characters", n, sha256.Size*2)
		}
		hash := [sha256.Size]byte(decoded)
		if names[name] {
			return nil, fmt.Errorf("keys line %d: duplicate client %q", n, name)
		}
		if _, dup := store.clients[hash]; dup {
			return nil, fmt.Errorf("keys line %d: duplicate key hash", n)
		}
		names[name] = true
		store.clients[hash] = name
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read keys: %w", err)
	}
	if len(store.clients) == 0 {
		return nil, errors.New("keys file has no keys")
	}
	return store, nil
}

// Len returns the number of registered keys.
func (s *Store) Len() int { return len(s.clients) }

// Lookup returns the client that owns key.
func (s *Store) Lookup(key string) (string, bool) {
	name, ok := s.clients[sha256.Sum256([]byte(key))]
	return name, ok
}

type clientKey struct{}

// ClientFrom returns the authenticated client name stored by Middleware.
func ClientFrom(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(clientKey{}).(string)
	return name, ok
}

// Middleware rejects requests without a valid "Authorization: Bearer <key>"
// header. Accepted requests carry the client name in their context and lose
// the Authorization header, so the key never reaches the upstream.
func Middleware(store *Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, key, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		client, ok := store.Lookup(key)
		if !strings.EqualFold(scheme, "Bearer") || key == "" || !ok {
			unauthorized(w)
			return
		}

		r = r.WithContext(context.WithValue(r.Context(), clientKey{}, client))
		r.Header.Del("Authorization")
		next.ServeHTTP(w, r)
	})
}

// unauthorized writes an OpenAI-style error so existing SDKs report it.
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	io.WriteString(w, `{"error":{"message":"Invalid or missing API key.","type":"invalid_request_error","code":"invalid_api_key"}}`)
}
