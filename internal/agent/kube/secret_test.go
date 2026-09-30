package kube

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"rampa.dev/pkg/protocol"
)

func typedNamespace(name string, lbls map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: lbls}}
}

func typedSecret(ns, name string, lbls map[string]string, data map[string]string) *corev1.Secret {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: lbls}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{}}
	for k, v := range data {
		s.Data[k] = []byte(v)
	}
	return s
}

func writeCmd(values map[string]string) *protocol.WriteSecretCommand {
	return &protocol.WriteSecretCommand{App: "checkout", Scope: "dev", Namespace: "checkout-dev", Target: TargetKubernetes, Values: values}
}

func (f *fixture) secret(t *testing.T, ns, name string) *corev1.Secret {
	t.Helper()
	s, err := f.typed.CoreV1().Secrets(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret %s/%s: %v", ns, name, err)
	}
	return s
}

func TestWriteSecretCreates(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil, typedNamespace("checkout-dev", map[string]string{LabelApp: "checkout"}))

	keys, err := f.client.WriteSecret(context.Background(), writeCmd(map[string]string{"DB_URL": "postgres://x", "API_KEY": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(keys, ",") != "API_KEY,DB_URL" {
		t.Errorf("claves ordenadas: %v", keys)
	}
	s := f.secret(t, "checkout-dev", "checkout-params")
	if s.Type != corev1.SecretTypeOpaque || string(s.Data["DB_URL"]) != "postgres://x" || string(s.Data["API_KEY"]) != "k" {
		t.Errorf("secret: %+v", s)
	}
	if s.Labels[LabelApp] != "checkout" || s.Labels[LabelScope] != "dev" || s.Labels[LabelManagedBy] != "rampa" {
		t.Errorf("labels: %v", s.Labels)
	}
}

func TestWriteSecretMergesKeys(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil,
		typedNamespace("checkout-dev", map[string]string{LabelApp: "checkout"}),
		typedSecret("checkout-dev", "checkout-params", nil, map[string]string{"A": "1", "B": "2"}),
	)

	keys, err := f.client.WriteSecret(context.Background(), writeCmd(map[string]string{"B": "nuevo", "C": "3"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(keys, ",") != "B,C" {
		t.Errorf("claves: %v", keys)
	}
	s := f.secret(t, "checkout-dev", "checkout-params")
	if string(s.Data["A"]) != "1" || string(s.Data["B"]) != "nuevo" || string(s.Data["C"]) != "3" || len(s.Data) != 3 {
		t.Errorf("merge: %v", s.Data)
	}
	// Un Secret preexistente sin labels queda adoptado.
	if s.Labels[LabelApp] != "checkout" || s.Labels[LabelManagedBy] != "rampa" {
		t.Errorf("labels tras adoptar: %v", s.Labels)
	}
}

func TestWriteSecretNamespaceGuards(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil,
		typedNamespace("checkout-dev", map[string]string{LabelApp: "ajeno"}),
		typedNamespace("checkout-prod", nil),
	)
	ctx := context.Background()

	cases := []struct {
		name string
		cmd  *protocol.WriteSecretCommand
		want string
	}{
		{"otra app", &protocol.WriteSecretCommand{App: "checkout", Scope: "dev", Namespace: "checkout-dev", Target: TargetKubernetes, Values: map[string]string{"A": "1"}}, `pertenece a la app "ajeno"`},
		{"namespace que no es app-scope", &protocol.WriteSecretCommand{App: "checkout", Scope: "dev", Namespace: "ajeno-dev", Target: TargetKubernetes, Values: map[string]string{"A": "1"}}, "no coincide con <app>-<scope>"},
		{"sin label", &protocol.WriteSecretCommand{App: "checkout", Scope: "prod", Namespace: "checkout-prod", Target: TargetKubernetes, Values: map[string]string{"A": "1"}}, "no lo creó Rampa"},
		{"sistema", &protocol.WriteSecretCommand{App: "checkout", Scope: "dev", Namespace: "kube-system", Target: TargetKubernetes, Values: map[string]string{"A": "1"}}, "namespace del sistema"},
		{"rampa-system", &protocol.WriteSecretCommand{App: "checkout", Scope: "dev", Namespace: "rampa-system", Target: TargetKubernetes, Values: map[string]string{"A": "1"}}, "namespace del sistema"},
		{"no existe y no sigue la convención", &protocol.WriteSecretCommand{App: "checkout", Scope: "dev", Namespace: "otro-nombre", Target: TargetKubernetes, Values: map[string]string{"A": "1"}}, "no coincide con <app>-<scope>"},
		{"target cloud", &protocol.WriteSecretCommand{App: "checkout", Scope: "dev", Namespace: "checkout-dev", Target: TargetCloud, Values: map[string]string{"A": "1"}}, `target "cloud"`},
		{"sin app", &protocol.WriteSecretCommand{Scope: "dev", Namespace: "checkout-dev", Target: TargetKubernetes, Values: map[string]string{"A": "1"}}, "no trae app"},
		{"sin namespace", &protocol.WriteSecretCommand{App: "checkout", Scope: "dev", Target: TargetKubernetes, Values: map[string]string{"A": "1"}}, "no trae namespace"},
		{"sin valores", writeCmd(nil), "no trae valores"},
		{"clave inválida", writeCmd(map[string]string{"con espacio": "1"}), "no es válida"},
		{"nil", nil, "está vacío"},
	}
	for _, tc := range cases {
		_, err := f.client.WriteSecret(ctx, tc.cmd)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: esperaba error con %q, fue %v", tc.name, tc.want, err)
		}
		// El mensaje de error nunca lleva el valor.
		if err != nil && strings.Contains(err.Error(), `"1"`) {
			t.Errorf("%s: el error contiene un valor: %v", tc.name, err)
		}
	}
	if _, err := f.typed.CoreV1().Secrets("checkout-dev").Get(ctx, "checkout-params", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("no debería haber escrito en checkout-dev: %v", err)
	}
}

func TestWriteSecretCreatesMissingNamespace(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil)

	keys, err := f.client.WriteSecret(context.Background(), writeCmd(map[string]string{"A": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Errorf("claves: %v", keys)
	}
	ns, err := f.typed.CoreV1().Namespaces().Get(context.Background(), "checkout-dev", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ns.Labels[LabelApp] != "checkout" || ns.Labels[LabelScope] != "dev" {
		t.Errorf("labels del namespace creado: %v", ns.Labels)
	}
	if s := f.secret(t, "checkout-dev", "checkout-params"); string(s.Data["A"]) != "1" {
		t.Errorf("secret: %v", s.Data)
	}
}

func TestWriteSecretRefusesForeignOrTypedSecrets(t *testing.T) {
	eso := typedSecret("checkout-dev", "checkout-params", map[string]string{LabelManagedBy: "external-secrets"}, map[string]string{"A": "1"})
	f := newFixture(t, fixedMapper(), nil, typedNamespace("checkout-dev", map[string]string{LabelApp: "checkout"}), eso)
	_, err := f.client.WriteSecret(context.Background(), writeCmd(map[string]string{"A": "2"}))
	if err == nil || !strings.Contains(err.Error(), `lo administra "external-secrets"`) {
		t.Errorf("esperaba rechazo por managed-by ajeno, fue %v", err)
	}
	if s := f.secret(t, "checkout-dev", "checkout-params"); string(s.Data["A"]) != "1" {
		t.Error("no debería haber tocado el Secret ajeno")
	}

	tls := typedSecret("checkout-dev", "checkout-params", nil, nil)
	tls.Type = corev1.SecretTypeTLS
	f = newFixture(t, fixedMapper(), nil, typedNamespace("checkout-dev", map[string]string{LabelApp: "checkout"}), tls)
	_, err = f.client.WriteSecret(context.Background(), writeCmd(map[string]string{"A": "2"}))
	if err == nil || !strings.Contains(err.Error(), "no Opaque") {
		t.Errorf("esperaba rechazo por tipo, fue %v", err)
	}

	deleting := typedNamespace("checkout-dev", map[string]string{LabelApp: "checkout"})
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	f = newFixture(t, fixedMapper(), nil, deleting)
	_, err = f.client.WriteSecret(context.Background(), writeCmd(map[string]string{"A": "2"}))
	if err == nil || !strings.Contains(err.Error(), "se está borrando") {
		t.Errorf("esperaba rechazo por namespace en borrado, fue %v", err)
	}
}

func TestWriteSecretRetriesOnConflict(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil,
		typedNamespace("checkout-dev", map[string]string{LabelApp: "checkout"}),
		typedSecret("checkout-dev", "checkout-params", nil, map[string]string{"A": "1"}),
	)
	var conflicts atomic.Int32
	f.typed.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		if conflicts.Add(1) == 1 {
			return true, nil, apierrors.NewConflict(corev1.Resource("secrets"), "checkout-params", nil)
		}
		return false, nil, nil
	})

	if _, err := f.client.WriteSecret(context.Background(), writeCmd(map[string]string{"B": "2"})); err != nil {
		t.Fatal(err)
	}
	if conflicts.Load() != 2 {
		t.Errorf("esperaba un reintento tras el conflicto: %d intentos", conflicts.Load())
	}
	if s := f.secret(t, "checkout-dev", "checkout-params"); string(s.Data["B"]) != "2" || string(s.Data["A"]) != "1" {
		t.Errorf("secret: %v", s.Data)
	}
}

func TestSecretKeys(t *testing.T) {
	keys, err := SecretKeys(map[string]string{"b": "", "A_1": "x", "c.d-e": "y"})
	if err != nil || strings.Join(keys, ",") != "A_1,b,c.d-e" {
		t.Errorf("claves: %v %v", keys, err)
	}
	for _, bad := range []string{"", "con espacio", "a/b", "ñ", "a=b"} {
		if _, err := SecretKeys(map[string]string{bad: "v"}); err == nil {
			t.Errorf("%q debería ser inválida", bad)
		}
	}
	if ParamsSecretName("checkout") != "checkout-params" {
		t.Error("ParamsSecretName")
	}
}

func TestWriteSecretAdoptsNamespaceCreatedByRampa(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil, typedNamespace("checkout-dev", map[string]string{LabelManagedBy: ManagedByValue}))
	if _, err := f.client.WriteSecret(context.Background(), writeCmd(map[string]string{"A": "1"})); err != nil {
		t.Fatal(err)
	}
}

func TestWriteSecretRejectsExternalSecretsOwned(t *testing.T) {
	ns := typedNamespace("checkout-dev", map[string]string{LabelApp: "checkout"})
	byLabel := typedSecret("checkout-dev", "checkout-params", map[string]string{"reconcile.external-secrets.io/managed": "true"}, map[string]string{"A": "1"})
	byOwner := typedSecret("checkout-dev", "checkout-params", nil, map[string]string{"A": "1"})
	byOwner.OwnerReferences = []metav1.OwnerReference{{APIVersion: "external-secrets.io/v1", Kind: "ExternalSecret", Name: "checkout-params", UID: "u"}}
	for name, sec := range map[string]*corev1.Secret{"label": byLabel, "owner": byOwner} {
		f := newFixture(t, fixedMapper(), nil, ns, sec)
		_, err := f.client.WriteSecret(context.Background(), writeCmd(map[string]string{"A": "2"}))
		if err == nil || !strings.Contains(err.Error(), "gestor de secretos de nube") {
			t.Errorf("%s: esperaba rechazo por ESO, fue %v", name, err)
		}
		if got := string(f.secret(t, "checkout-dev", "checkout-params").Data["A"]); got != "1" {
			t.Errorf("%s: el secret cambió a %q", name, got)
		}
	}
}

func TestSecretKeysLimits(t *testing.T) {
	many := map[string]string{}
	for i := 0; i <= MaxSecretKeys; i++ {
		many[fmt.Sprintf("K%d", i)] = "v"
	}
	if _, err := SecretKeys(many); err == nil || !strings.Contains(err.Error(), "máximo es 50") {
		t.Errorf("demasiadas claves: %v", err)
	}
	delete(many, "K0")
	if _, err := SecretKeys(many); err != nil {
		t.Errorf("50 claves deben pasar: %v", err)
	}
	big := map[string]string{"BIG": strings.Repeat("x", MaxSecretValueBytes+1)}
	if _, err := SecretKeys(big); err == nil || !strings.Contains(err.Error(), "BIG") || strings.Contains(err.Error(), "xxxx") {
		t.Errorf("valor grande: %v", err)
	}
	ok := map[string]string{"OK": strings.Repeat("x", MaxSecretValueBytes)}
	if _, err := SecretKeys(ok); err != nil {
		t.Errorf("64 KiB exactos deben pasar: %v", err)
	}
}
