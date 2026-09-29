package server

import "rampa.dev/internal/api/auth"

func hashOf(token string) string { return auth.HashToken(token) }
