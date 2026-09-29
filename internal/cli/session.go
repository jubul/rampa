package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rampa.dev/internal/cli/apiclient"
	"rampa.dev/internal/cli/config"
	"rampa.dev/pkg/manifest"
)

// cliVersion la setea Execute; va en el User-Agent.
var cliVersion = "dev"

// requestTimeout es el timeout de los requests normales del CLI.
const requestTimeout = 30 * time.Second

// session es lo que un comando necesita para hablar con la API.
type session struct {
	client *apiclient.Client
	org    string
}

// errNoOrg indica que hay sesión pero no se pudo resolver la organización.
var errNoOrg = errors.New("no sé sobre qué organización trabajar: pasá --org, exportá RAMPA_ORG o corré `rampa login --org SLUG`")

// newSession carga la config, exige sesión y resuelve la organización.
// orgFlag pisa a la config. Devuelve un error accionable si falta algo.
func newSession(orgFlag string) (*session, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if !cfg.HasSession() {
		return nil, config.ErrNoSession
	}
	org := orgFlag
	if org == "" {
		org = cfg.Org
	}
	if org == "" {
		return nil, errNoOrg
	}
	c := apiclient.New(cfg.API, cfg.Token).WithUserAgent("rampa-cli/" + cliVersion).WithTimeout(requestTimeout)
	return &session{client: c, org: org}, nil
}

// optionalSession devuelve nil (sin error) si no hay sesión configurada.
// Sirve para comandos que funcionan offline y mejoran con la API.
func optionalSession(orgFlag string) (*session, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if !cfg.HasSession() {
		return nil, nil
	}
	return newSession(orgFlag)
}

// requestCtx arma un contexto con timeout para un request normal.
func requestCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, requestTimeout)
}

// resolveAppName devuelve el nombre de la app: --app si vino, si no
// metadata.name del rampa.yaml.
func resolveAppName(appFlag, file string) (string, error) {
	if appFlag != "" {
		return appFlag, nil
	}
	app, err := manifest.Load(file)
	if err != nil {
		return "", fmt.Errorf("necesito --app o un rampa.yaml legible (-f): %w", err)
	}
	if app.Metadata.Name == "" {
		return "", fmt.Errorf("%s no tiene metadata.name; pasá --app", file)
	}
	return app.Metadata.Name, nil
}

// addOrgFlag agrega --org a un comando que habla con la API.
func addOrgFlag(c *cobra.Command, org *string) {
	c.Flags().StringVar(org, "org", "", "slug de la organización (default: RAMPA_ORG o el org del login)")
}

// logger devuelve un slog que escribe en stderr, con nivel debug si RAMPA_DEBUG está seteada.
func logger(cmd *cobra.Command) *slog.Logger {
	lvl := slog.LevelWarn
	if v := strings.ToLower(os.Getenv("RAMPA_DEBUG")); v == "1" || v == "true" {
		lvl = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: lvl}))
}

// warnDeprecated avisa por stderr que un flag es del modo GitOps legado.
func warnDeprecated(cmd *cobra.Command, flag string) {
	fmt.Fprintf(cmd.ErrOrStderr(), "aviso: --%s es el modo GitOps legado (rampa-deploys + Argo CD) y va a desaparecer. Migrá a `rampa login` y la API.\n", flag)
}
