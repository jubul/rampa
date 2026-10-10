package infra

import "testing"

func TestTr(t *testing.T) {
	if tr("en", "hola", "hello") != "hello" || tr("es", "hola", "hello") != "hola" || tr("", "hola", "hello") != "hola" {
		t.Error("tr no elige el idioma esperado")
	}
}
