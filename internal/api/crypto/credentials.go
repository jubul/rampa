package crypto

import (
	"encoding/json"
	"errors"
	"fmt"
)

// SealCredentials cifra las credenciales de una cuenta de nube (los campos
// marcados secret) como un JSON, con el id de la cuenta como dato autenticado.
func (c *Cipher) SealCredentials(accountID string, creds map[string]string) ([]byte, error) {
	if c == nil {
		return nil, errors.New("sin clave de cifrado (RAMPA_STATE_KEY)")
	}
	if accountID == "" {
		return nil, errors.New("las credenciales necesitan el id de la cuenta como dato autenticado")
	}
	if creds == nil {
		creds = map[string]string{}
	}
	plain, err := json.Marshal(creds)
	if err != nil {
		return nil, fmt.Errorf("serializando credenciales: %w", err)
	}
	return c.Seal(plain, []byte(accountID))
}

// OpenCredentials descifra lo que devolvió SealCredentials para la misma cuenta.
func (c *Cipher) OpenCredentials(accountID string, ciphertext []byte) (map[string]string, error) {
	if c == nil {
		return nil, errors.New("sin clave de cifrado (RAMPA_STATE_KEY)")
	}
	plain, err := c.Open(ciphertext, []byte(accountID))
	if err != nil {
		return nil, fmt.Errorf("credenciales de la cuenta %s: %w", accountID, err)
	}
	creds := map[string]string{}
	if err := json.Unmarshal(plain, &creds); err != nil {
		return nil, fmt.Errorf("credenciales de la cuenta %s corruptas: %w", accountID, err)
	}
	return creds, nil
}
