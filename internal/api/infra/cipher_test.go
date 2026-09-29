package infra

import (
	"bytes"
	"strings"
	"testing"
)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestCipherRoundTrip(t *testing.T) {
	c, err := NewCipher(testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"version":4,"resources":[{"type":"aws_eks_cluster"}]}`)
	ct, err := c.Seal(plain, []byte("run-1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("aws_eks_cluster")) {
		t.Error("el texto cifrado contiene el texto plano")
	}
	ct2, _ := c.Seal(plain, []byte("run-1"))
	if bytes.Equal(ct, ct2) {
		t.Error("dos Seal del mismo texto deberían diferir (nonce aleatorio)")
	}
	got, err := c.Open(ct, []byte("run-1"))
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("Open: %v %q", err, got)
	}
	if _, err := c.Open(ct, []byte("run-2")); err == nil {
		t.Error("otro run (aad) no debería poder descifrar")
	}
	other, _ := NewCipher(testKey(2))
	if _, err := other.Open(ct, []byte("run-1")); err == nil || !strings.Contains(err.Error(), "RAMPA_STATE_KEY") {
		t.Errorf("otra clave debería fallar con un mensaje claro: %v", err)
	}
	if _, err := c.Open(ct[:5], []byte("run-1")); err == nil {
		t.Error("dato corto debería fallar")
	}
	if _, err := NewCipher([]byte("corta")); err == nil {
		t.Error("clave de largo incorrecto debería fallar")
	}
}
