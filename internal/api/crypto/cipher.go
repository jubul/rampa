// Package crypto es el cifrado simétrico compartido del plano de control:
// AES-256-GCM con la clave del servidor (RAMPA_STATE_KEY). Lo usan el backend
// de estado de OpenTofu (infra_states) y las credenciales de las cuentas de
// nube con auth token (cloud_accounts.credentials_ciphertext).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeyBytes es el largo de la clave AES-256.
const KeyBytes = 32

// Cipher cifra con AES-256-GCM. Cada Seal usa un nonce aleatorio que se
// antepone al texto cifrado; el llamador pasa un dato autenticado adicional
// (el id del run, el id de la cuenta) para que un ciphertext no se pueda mover
// de una fila a otra.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher arma el Cipher con una clave de 32 bytes.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != KeyBytes {
		return nil, fmt.Errorf("la clave tiene %d bytes; AES-256 necesita %d", len(key), KeyBytes)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Seal devuelve nonce || ciphertext.
func (c *Cipher) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generando el nonce: %w", err)
	}
	out := make([]byte, 0, len(nonce)+len(plaintext)+c.aead.Overhead())
	out = append(out, nonce...)
	return c.aead.Seal(out, nonce, plaintext, aad), nil
}

// Open descifra lo que devolvió Seal. Falla si la clave, el nonce, el aad o
// el contenido no coinciden.
func (c *Cipher) Open(data, aad []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(data) < ns+c.aead.Overhead() {
		return nil, errors.New("dato cifrado demasiado corto")
	}
	plain, err := c.aead.Open(nil, data[:ns], data[ns:], aad)
	if err != nil {
		return nil, errors.New("no pude descifrar: la clave RAMPA_STATE_KEY no es la que lo cifró o el dato está corrupto")
	}
	return plain, nil
}
