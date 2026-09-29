package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationLockID es la clave del advisory lock que evita que dos instancias
// corran migraciones a la vez.
const migrationLockID = 727262

// Migrate aplica las migraciones pendientes en orden. Lleva el registro en
// schema_migrations. Es idempotente.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("listando migraciones: %w", err)
	}
	sort.Strings(names)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("conectando para migrar: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("tomando el lock de migraciones: %w", err)
	}
	defer func() {
		// Liberamos con un contexto propio: si el ctx original ya venció, el lock
		// se libera igual al cerrar la conexión, pero mejor ser explícitos.
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockID)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("creando schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("leyendo schema_migrations: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, name := range names {
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		if applied[version] {
			continue
		}
		sqlText, err := migrationsFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("leyendo %s: %w", name, err)
		}
		if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sqlText)); err != nil {
				return fmt.Errorf("aplicando %s: %w", version, err)
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version)
			return err
		}); err != nil {
			return err
		}
		if log != nil {
			log.Info("migración aplicada", "version", version)
		}
	}
	return nil
}

// MigrationVersions devuelve los nombres de las migraciones embebidas, ordenados.
func MigrationVersions() []string {
	names, _ := fs.Glob(migrationsFS, "migrations/*.sql")
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(n, "migrations/"), ".sql"))
	}
	return out
}
