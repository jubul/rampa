package cloud

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/smithy-go"

	"rampa.dev/pkg/protocol"
)

// mockSM guarda secretos en memoria y registra las llamadas.
type mockSM struct {
	store   map[string]string
	tags    map[string]map[string]string
	creates []string
	puts    []string
	denyAll bool
	failPut error
}

func newMock(existing map[string]string) *mockSM {
	m := &mockSM{store: map[string]string{}, tags: map[string]map[string]string{}}
	for k, v := range existing {
		m.store[k] = v
	}
	return m
}

func (m *mockSM) CreateSecret(_ context.Context, in *secretsmanager.CreateSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error) {
	name := aws.ToString(in.Name)
	m.creates = append(m.creates, name)
	if m.denyAll {
		return nil, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:sts::123:assumed-role/rampa-agent is not authorized to perform: secretsmanager:CreateSecret"}
	}
	if _, ok := m.store[name]; ok {
		return nil, &types.ResourceExistsException{Message: aws.String("ya existe")}
	}
	m.store[name] = aws.ToString(in.SecretString)
	m.tags[name] = map[string]string{}
	for _, t := range in.Tags {
		m.tags[name][aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return &secretsmanager.CreateSecretOutput{Name: in.Name}, nil
}

func (m *mockSM) PutSecretValue(_ context.Context, in *secretsmanager.PutSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error) {
	name := aws.ToString(in.SecretId)
	m.puts = append(m.puts, name)
	if m.failPut != nil {
		return nil, m.failPut
	}
	if _, ok := m.store[name]; !ok {
		return nil, &types.ResourceNotFoundException{Message: aws.String("no existe")}
	}
	m.store[name] = aws.ToString(in.SecretString)
	return &secretsmanager.PutSecretValueOutput{Name: in.SecretId}, nil
}

func cloudCmd(values map[string]string) *protocol.WriteSecretCommand {
	return &protocol.WriteSecretCommand{App: "checkout", Scope: "prod", Namespace: "checkout-prod", Target: "cloud", Values: values}
}

func TestWriteSecretCreateAndPut(t *testing.T) {
	m := newMock(map[string]string{"rampa/checkout/prod/DB_URL": "viejo"})
	var logs bytes.Buffer
	w := NewAWSSecretsManagerWithAPI(m, slog.New(slog.NewTextHandler(&logs, nil)))

	keys, err := w.WriteSecret(context.Background(), cloudCmd(map[string]string{"DB_URL": "postgres://nuevo", "API_KEY": "k-secreta"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(keys, ",") != "API_KEY,DB_URL" {
		t.Errorf("claves: %v", keys)
	}
	if m.store["rampa/checkout/prod/API_KEY"] != "k-secreta" || m.store["rampa/checkout/prod/DB_URL"] != "postgres://nuevo" {
		t.Errorf("store: %v", m.store)
	}
	// API_KEY es nuevo: Create. DB_URL existía: Create falla con ResourceExists y sigue Put.
	if strings.Join(m.creates, ",") != "rampa/checkout/prod/API_KEY,rampa/checkout/prod/DB_URL" || strings.Join(m.puts, ",") != "rampa/checkout/prod/DB_URL" {
		t.Errorf("llamadas: creates=%v puts=%v", m.creates, m.puts)
	}
	if tags := m.tags["rampa/checkout/prod/API_KEY"]; tags[TagApp] != "checkout" || tags[TagScope] != "prod" {
		t.Errorf("tags: %v", tags)
	}
	for _, v := range []string{"k-secreta", "postgres://nuevo"} {
		if strings.Contains(logs.String(), v) {
			t.Errorf("el log contiene un valor: %s", logs.String())
		}
	}
}

func TestWriteSecretAccessDenied(t *testing.T) {
	m := newMock(nil)
	m.denyAll = true
	w := NewAWSSecretsManagerWithAPI(m, nil)

	_, err := w.WriteSecret(context.Background(), cloudCmd(map[string]string{"API_KEY": "k-secreta"}))
	if err == nil {
		t.Fatal("esperaba error")
	}
	for _, want := range []string{"no tiene permiso secretsmanager:CreateSecret", "rampa/checkout/prod/API_KEY", "rampa/*"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("el error debería mencionar %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "k-secreta") {
		t.Errorf("el error contiene el valor: %v", err)
	}
}

func TestWriteSecretOtherErrors(t *testing.T) {
	m := newMock(map[string]string{"rampa/checkout/prod/A": "1"})
	m.failPut = &smithy.GenericAPIError{Code: "InvalidRequestException", Message: "scheduled for deletion"}
	w := NewAWSSecretsManagerWithAPI(m, nil)
	_, err := w.WriteSecret(context.Background(), cloudCmd(map[string]string{"A": "2"}))
	if err == nil || !strings.Contains(err.Error(), "PutSecretValue") || !strings.Contains(err.Error(), "InvalidRequestException") {
		t.Errorf("error de put: %v", err)
	}

	m.failPut = errors.New("dial tcp: i/o timeout")
	_, err = w.WriteSecret(context.Background(), cloudCmd(map[string]string{"A": "2"}))
	if err == nil || !strings.Contains(err.Error(), "i/o timeout") {
		t.Errorf("error genérico: %v", err)
	}

	w = &AWSSecretsManager{newAPI: func(context.Context) (SecretsManagerAPI, error) {
		return nil, errors.New("failed to retrieve credentials: no EC2 IMDS role found")
	}}
	_, err = w.WriteSecret(context.Background(), cloudCmd(map[string]string{"A": "2"}))
	if err == nil || !strings.Contains(err.Error(), "no tiene identidad de AWS") {
		t.Errorf("sin credenciales: %v", err)
	}

	w = &AWSSecretsManager{newAPI: func(context.Context) (SecretsManagerAPI, error) { return nil, &aws.MissingRegionError{} }}
	_, err = w.WriteSecret(context.Background(), cloudCmd(map[string]string{"A": "2"}))
	if err == nil || !strings.Contains(err.Error(), "AWS_REGION") {
		t.Errorf("sin región: %v", err)
	}
}

func TestWriteSecretValidation(t *testing.T) {
	m := newMock(nil)
	w := NewAWSSecretsManagerWithAPI(m, nil)
	cases := []struct {
		cmd  *protocol.WriteSecretCommand
		want string
	}{
		{nil, "está vacío"},
		{&protocol.WriteSecretCommand{App: "a", Scope: "s", Target: "kubernetes", Values: map[string]string{"A": "1"}}, `target "kubernetes"`},
		{&protocol.WriteSecretCommand{Scope: "s", Target: "cloud", Values: map[string]string{"A": "1"}}, "no trae app"},
		{&protocol.WriteSecretCommand{App: "a", Target: "cloud", Values: map[string]string{"A": "1"}}, "no trae scope"},
		{&protocol.WriteSecretCommand{App: "a/b", Scope: "s", Target: "cloud", Values: map[string]string{"A": "1"}}, "barras"},
		{cloudCmd(nil), "no trae valores"},
		{cloudCmd(map[string]string{"con/barra": "1"}), "no es válida"},
	}
	for _, tc := range cases {
		_, err := w.WriteSecret(context.Background(), tc.cmd)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("esperaba %q, fue %v", tc.want, err)
		}
	}
	if len(m.creates) != 0 || len(m.puts) != 0 {
		t.Errorf("no debería haber llamado a AWS: %v %v", m.creates, m.puts)
	}
	if SecretPath("app", "dev", "KEY") != "rampa/app/dev/KEY" {
		t.Error("SecretPath")
	}
}

// El cliente por defecto se arma una sola vez y se reutiliza.
func TestClientIsLazyAndCached(t *testing.T) {
	m := newMock(nil)
	calls := 0
	w := &AWSSecretsManager{newAPI: func(context.Context) (SecretsManagerAPI, error) { calls++; return m, nil }}
	for i := 0; i < 3; i++ {
		if _, err := w.WriteSecret(context.Background(), cloudCmd(map[string]string{"A": "1"})); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("el cliente debería armarse una vez: %d", calls)
	}
}

func TestWriteSecretPartialFailureReturnsWrittenKeys(t *testing.T) {
	m := newMock(map[string]string{"rampa/checkout/prod/B": "1"})
	m.failPut = errors.New("boom")
	w := NewAWSSecretsManagerWithAPI(m, nil)
	// A se crea, B ya existe y su put falla, C no se intenta.
	keys, err := w.WriteSecret(context.Background(), cloudCmd(map[string]string{"A": "1", "B": "2", "C": "3"}))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("esperaba error: %v", err)
	}
	if strings.Join(keys, ",") != "A" {
		t.Errorf("claves escritas: %v", keys)
	}
}
