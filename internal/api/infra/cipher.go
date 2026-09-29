package infra

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// Cipher cifra el estado de OpenTofu con AES-256-GCM. Cada Seal usa un nonce
// aleatorio que se antepone al texto cifrado; el id del run va como dato
// autenticado adicional, así un estado no se puede mover de un run a otro.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher arma el Cipher con una clave de 32 bytes.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("la clave de estado tiene %d bytes; AES-256 necesita 32", len(key))
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
		return nil, errors.New("estado cifrado demasiado corto")
	}
	plain, err := c.aead.Open(nil, data[:ns], data[ns:], aad)
	if err != nil {
		return nil, errors.New("no pude descifrar el estado: la clave RAMPA_STATE_KEY no es la que lo cifró o el dato está corrupto")
	}
	return plain, nil
}
