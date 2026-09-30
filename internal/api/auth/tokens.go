package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Prefijos de los tokens. Sirven para reconocerlos en logs y en el header.
const (
	APITokenPrefix     = "rampa_"
	ClusterTokenPrefix = "rampa_cluster_"
	// BootstrapTokenPrefix identifica los tokens de bootstrap de un solo uso con
	// los que se instala el agente (ADR 0004): se canjean por el token del cluster.
	BootstrapTokenPrefix = "rampa_bootstrap_"
)

const tokenRandomBytes = 32

// NewAPIToken genera un token "rampa_<random>" y devuelve el texto plano y su hash.
func NewAPIToken() (plain, hash string, err error) {
	return newToken(APITokenPrefix)
}

// NewClusterToken genera un token "rampa_cluster_<random>" y devuelve el texto plano y su hash.
func NewClusterToken() (plain, hash string, err error) {
	return newToken(ClusterTokenPrefix)
}

// NewBootstrapToken genera un token "rampa_bootstrap_<random>" y devuelve el texto plano y su hash.
func NewBootstrapToken() (plain, hash string, err error) {
	return newToken(BootstrapTokenPrefix)
}

func newToken(prefix string) (string, string, error) {
	b := make([]byte, tokenRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generando token: %w", err)
	}
	plain := prefix + hex.EncodeToString(b)
	return plain, HashToken(plain), nil
}

// HashToken es SHA-256 en hex del token en texto plano. Es lo único que se guarda.
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// IsClusterToken dice si el texto tiene forma de token de cluster.
func IsClusterToken(plain string) bool {
	return strings.HasPrefix(plain, ClusterTokenPrefix)
}

// IsBootstrapToken dice si el texto tiene forma de token de bootstrap.
func IsBootstrapToken(plain string) bool {
	return strings.HasPrefix(plain, BootstrapTokenPrefix)
}

// IsAPIToken dice si el texto tiene forma de token de API (y no de cluster ni de bootstrap).
func IsAPIToken(plain string) bool {
	return strings.HasPrefix(plain, APITokenPrefix) && !IsClusterToken(plain) && !IsBootstrapToken(plain)
}

// BearerToken extrae el token de un header Authorization: Bearer <token>.
func BearerToken(header string) (string, bool) {
	const scheme = "bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	tok := strings.TrimSpace(header[len(scheme):])
	return tok, tok != ""
}
