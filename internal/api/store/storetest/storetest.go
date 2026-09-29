// Package storetest levanta un Postgres embebido para los tests de los
// paquetes que necesitan base de datos (store, infra, server). Se arranca una
// sola vez por proceso de test, en un puerto libre y un directorio temporal;
// cada test pide una base propia con NewDatabase. Con RAMPA_TEST_SKIP_DB=1 o si
// los binarios no se pueden descargar, los tests se saltan con un mensaje claro.
package storetest

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"
)

var (
	once    sync.Once
	admin   string // URL de la base de mantenimiento
	skip    string // motivo para saltar
	stop    func()
	counter int
	mu      sync.Mutex
)

func start() {
	if os.Getenv("RAMPA_TEST_SKIP_DB") == "1" {
		skip = "RAMPA_TEST_SKIP_DB=1: se saltan los tests con base de datos"
		return
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		skip = "no pude reservar un puerto: " + err.Error()
		return
	}
	port := uint32(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	dir, err := os.MkdirTemp("", "rampa-pg-")
	if err != nil {
		skip = "no pude crear el directorio temporal: " + err.Error()
		return
	}
	cfg := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(port).
		Database("postgres").
		Username("rampa").
		Password("rampa").
		DataPath(filepath.Join(dir, "data")).
		RuntimePath(filepath.Join(dir, "runtime")).
		StartTimeout(90 * time.Second).
		Logger(io.Discard)
	pg := embeddedpostgres.NewDatabase(cfg)
	if err := pg.Start(); err != nil {
		skip = fmt.Sprintf("no pude arrancar el Postgres embebido (hace falta red para descargar los binarios la primera vez): %v", err)
		_ = os.RemoveAll(dir)
		return
	}
	stop = func() {
		_ = pg.Stop()
		_ = os.RemoveAll(dir)
	}
	admin = fmt.Sprintf("postgres://rampa:rampa@127.0.0.1:%d/postgres?sslmode=disable", port)
}

// NewDatabase devuelve la URL de una base nueva y vacía para el test, o lo
// salta si no hay Postgres disponible.
func NewDatabase(t *testing.T) string {
	t.Helper()
	once.Do(start)
	if skip != "" {
		t.Skip(skip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("conectando a la base de mantenimiento: %v", err)
	}
	defer conn.Close(ctx)
	mu.Lock()
	counter++
	name := fmt.Sprintf("rampa_test_%d_%d", os.Getpid(), counter)
	mu.Unlock()
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creando la base %s: %v", name, err)
	}
	return strings.Replace(admin, "/postgres?", "/"+name+"?", 1)
}

// Stop apaga el Postgres compartido. Llamarlo desde TestMain después de m.Run().
func Stop() {
	if stop != nil {
		stop()
	}
}
