package provider

import (
	"context"
	"testing"
)

// fake es un provider de mentira para probar el registro.
type fake struct {
	kind      Kind
	available bool
}

func (f fake) Kind() Kind                                      { return f.kind }
func (f fake) DisplayName() string                             { return "Fake " + string(f.kind) }
func (f fake) Available() bool                                 { return f.available }
func (f fake) Auth() AuthKind                                  { return AuthFederated }
func (f fake) Terminology() Terminology                        { return Terminology{} }
func (f fake) ConnectInstructions(ConnectRequest) Instructions { return Instructions{Title: "fake"} }
func (f fake) Verify(context.Context, CloudAccount) (Verification, error) {
	return Verification{OK: f.available}, nil
}
func (f fake) Registry(CloudAccount) (string, error) { return "registry." + string(f.kind), nil }
func (f fake) SecretStoreSpec(CloudAccount, ClusterInfo) SecretStoreSpec {
	return SecretStoreSpec{Name: string(f.kind)}
}
func (f fake) WorkloadIdentityAnnotations(CloudAccount, string) map[string]string {
	return map[string]string{}
}
func (f fake) ServiceModule(string) (ModuleRef, bool) { return ModuleRef{}, false }

// resetRegistry deja el registro vacío y devuelve una función que lo restaura.
func resetRegistry(t *testing.T) {
	t.Helper()
	mu.Lock()
	saved := registry
	registry = map[Kind]Provider{}
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		registry = saved
		mu.Unlock()
	})
}

func TestRegisterAndGet(t *testing.T) {
	resetRegistry(t)

	if _, ok := Get(Kind("zeta")); ok {
		t.Fatal("el registro debería estar vacío")
	}
	Register(fake{kind: "zeta", available: true})
	p, ok := Get(Kind("zeta"))
	if !ok {
		t.Fatal("no encontré el provider registrado")
	}
	if !p.Available() || p.DisplayName() != "Fake zeta" {
		t.Errorf("provider distinto al registrado: %+v", p)
	}

	// Registrar el mismo kind de nuevo lo reemplaza, no lo duplica.
	Register(fake{kind: "zeta", available: false})
	p, _ = Get(Kind("zeta"))
	if p.Available() {
		t.Error("Register debería reemplazar al provider anterior")
	}
	if got := len(All()); got != 1 {
		t.Errorf("All() tiene %d providers, esperaba 1", got)
	}
}

func TestAllSorted(t *testing.T) {
	resetRegistry(t)
	for _, k := range []Kind{"gamma", "alpha", "beta"} {
		Register(fake{kind: k})
	}
	all := All()
	if len(all) != 3 {
		t.Fatalf("All() = %d", len(all))
	}
	for i, want := range []Kind{"alpha", "beta", "gamma"} {
		if all[i].Kind() != want {
			t.Errorf("All()[%d] = %s, quería %s", i, all[i].Kind(), want)
		}
	}
}

func TestRestoresRealRegistry(t *testing.T) {
	// Después de los tests anteriores, el registro original tiene que seguir intacto
	// (puede estar vacío en este paquete porque los subpaquetes no se importan acá).
	all := All()
	for _, p := range all {
		if p.Kind() == "alpha" || p.Kind() == "zeta" {
			t.Errorf("quedó un provider falso en el registro: %s", p.Kind())
		}
	}
}
