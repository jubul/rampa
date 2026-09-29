// Package store es el acceso a Postgres del plano de control: pool, migraciones
// y repositorios por entidad. Todos los métodos que tocan datos de una
// organización reciben orgID y filtran por él; las excepciones son users,
// sessions y las búsquedas de tokens por hash, que son globales por diseño.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errores que los handlers traducen a códigos HTTP.
var (
	ErrNotFound = errors.New("no encontrado")
	ErrConflict = errors.New("ya existe")
)

// dbtx es lo que tienen en común un pool y una transacción.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store agrupa los repositorios. Se crea con Open y se comparte.
type Store struct {
	pool *pgxpool.Pool
	db   dbtx
}

// Open abre el pool contra databaseURL y verifica la conexión.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("RAMPA_DATABASE_URL inválida: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnLifetime = time.Hour
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("abriendo el pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("no pude conectarme a Postgres: %w", err)
	}
	return &Store{pool: pool, db: pool}, nil
}

// Pool expone el pool, para migraciones y para readyz.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Close cierra el pool.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Ping verifica que la base responda.
func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.pool.Ping(ctx)
}

// WithTx corre fn dentro de una transacción. El Store que recibe fn está
// atado a la transacción, así que todos sus métodos participan de ella.
func (s *Store) WithTx(ctx context.Context, fn func(tx *Store) error) error {
	if s.pool == nil {
		return errors.New("WithTx sin pool")
	}
	if _, already := s.db.(pgx.Tx); already {
		// Ya estamos en una transacción: reusamos.
		return fn(s)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(&Store{pool: s.pool, db: tx})
	})
}

// isUniqueViolation detecta el error 23505 de Postgres.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isForeignKeyViolation detecta el error 23503 de Postgres.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// translate convierte errores de pgx en los errores del paquete.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case isUniqueViolation(err):
		return ErrConflict
	case isForeignKeyViolation(err):
		return ErrConflict
	default:
		return err
	}
}

// nullTime convierte *time.Time a algo insertable.
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

// nullString convierte "" a NULL para columnas uuid opcionales.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ptrString convierte *string a string.
func ptrString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
