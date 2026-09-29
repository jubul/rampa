package provider

import (
	"context"
	"time"
)

// Credentials son credenciales temporales para operar en la cuenta del
// cliente desde fuera del cluster (el runner de infraestructura las exporta al
// subproceso de OpenTofu). Nunca se persisten ni se loguean.
type Credentials struct {
	// AccessKeyID, SecretAccessKey y SessionToken son el trío clásico de AWS.
	// Otras nubes pueden dejarlos vacíos y usar solo Env.
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	// Expires es cuándo dejan de valer.
	Expires time.Time
	// Env son variables de entorno adicionales que el subproceso necesita
	// (por ejemplo AWS_REGION, o el token federado de otra nube).
	Env map[string]string
}

// CredentialsProvider lo implementan los providers que pueden emitir
// credenciales temporales asumiendo la identidad de la cuenta. Es opcional: el
// runner hace un type assertion y, si el provider no lo implementa, el run
// falla con un mensaje claro.
type CredentialsProvider interface {
	Credentials(ctx context.Context, acc CloudAccount) (Credentials, error)
}

type runIDKey struct{}

// WithRunID anota el contexto con el id del run de infraestructura que pide
// las credenciales, para que el provider lo use en el nombre de sesión y
// quede en el CloudTrail del cliente quién hizo qué.
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDKey{}, runID)
}

// RunIDFrom devuelve el id del run anotado con WithRunID, o "".
func RunIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(runIDKey{}).(string)
	return v
}
