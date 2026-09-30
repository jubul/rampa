package infra

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rampa.dev/internal/api/crypto"
	"rampa.dev/internal/api/events"
	"rampa.dev/internal/api/store"
	"rampa.dev/internal/api/store/storetest"
)

// testKey es una clave AES-256 de prueba.
func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestMain(m *testing.M) {
	code := m.Run()
	storetest.Stop()
	os.Exit(code)
}

// fixture es una base migrada con org, usuario y cuenta de nube conectada.
type fixture struct {
	t     *testing.T
	st    *store.Store
	bus   *events.Bus
	orgID string
	user  *store.User
	acc   *store.CloudAccount
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dbURL := storetest.NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(ctx, st.Pool(), nil); err != nil {
		t.Fatal(err)
	}
	user, err := st.UpsertUser(ctx, "infra@acme.com", "Infra")
	if err != nil {
		t.Fatal(err)
	}
	// Cada test tiene su propia base, así que el slug puede repetirse.
	org, err := st.CreateOrg(ctx, "acme", "Acme", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, st: st, bus: events.New(st, quietLog()), orgID: org.ID, user: user}
	f.acc = f.connectedAccount("acme-aws")
	return f
}

func (f *fixture) connectedAccount(name string) *store.CloudAccount {
	f.t.Helper()
	ctx := context.Background()
	acc, err := f.st.CreateCloudAccount(ctx, &store.CloudAccount{OrgID: f.orgID, Provider: "aws", Name: name, Region: "us-east-1", Settings: map[string]string{"external_id": "ext-1"}})
	if err != nil {
		f.t.Fatal(err)
	}
	acc, err = f.st.UpdateCloudAccountRef(ctx, f.orgID, acc.ID, "arn:aws:iam::123456789012:role/RampaConnect", acc.Settings)
	if err != nil {
		f.t.Fatal(err)
	}
	acc, err = f.st.SetCloudAccountVerification(ctx, f.orgID, acc.ID, store.CloudAccountConnected, store.Verification{OK: true})
	if err != nil {
		f.t.Fatal(err)
	}
	return acc
}

// tokenAccount crea una cuenta de un provider con auth token (digitalocean)
// con sus credenciales cifradas con key, ya connected.
func (f *fixture) tokenAccount(name, providerKind string, key []byte, creds, settings map[string]string) *store.CloudAccount {
	f.t.Helper()
	ctx := context.Background()
	if settings == nil {
		settings = map[string]string{}
	}
	acc, err := f.st.CreateCloudAccount(ctx, &store.CloudAccount{OrgID: f.orgID, Provider: providerKind, Name: name, Region: "nyc3", Settings: settings})
	if err != nil {
		f.t.Fatal(err)
	}
	c, err := crypto.NewCipher(key)
	if err != nil {
		f.t.Fatal(err)
	}
	ct, err := c.SealCredentials(acc.ID, creds)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.st.SetCloudAccountCredentials(ctx, f.orgID, acc.ID, ct); err != nil {
		f.t.Fatal(err)
	}
	acc, err = f.st.SetCloudAccountVerification(ctx, f.orgID, acc.ID, store.CloudAccountConnected, store.Verification{OK: true})
	if err != nil {
		f.t.Fatal(err)
	}
	return acc
}

// fakeTofu es la ruta absoluta del tofu falso de testdata.
func fakeTofu(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "fake-tofu.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout esperando: %s", what)
}

// quietLog solo muestra warnings y errores.
func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}
