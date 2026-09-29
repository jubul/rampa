package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"rampa.dev/internal/cli/apiclient"
	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/render"
)

// deployPollInterval es cada cuánto, además del stream, se consulta el
// deployment por GET durante --wait. Cubre el caso en que el evento terminal
// se emitió entre el POST y la conexión al stream.
var deployPollInterval = 20 * time.Second

// streamRetryDelay es la pausa antes de reconectar si el servidor cierra el stream.
var streamRetryDelay = 2 * time.Second

// streamBackoffMin y streamBackoffMax acotan el backoff exponencial con el que
// se reconecta al stream tras un error de transporte o un 5xx.
var (
	streamBackoffMin = 1 * time.Second
	streamBackoffMax = 30 * time.Second
)

// isFatalStreamError dice si un error del stream no tiene sentido reintentarlo:
// credenciales inválidas, sin permiso o deployment inexistente.
func isFatalStreamError(err error) bool {
	var ae *apiclient.Error
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.Status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}
	return false
}

func newDeployCmd() *cobra.Command {
	var file, appName, scope, image, message, org, deploys, platform string
	var wait bool
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "deploy",
		Short: "Despliega una imagen a un scope a través de la API",
		Long: `Pide a la API un deployment: POST /v1/orgs/{org}/apps/{app}/scopes/{scope}/deployments.
Con --wait sigue el stream de eventos hasta healthy (exit 0) o failed/cancelled (exit 1).
Necesita sesión: rampa login, o RAMPA_API, RAMPA_TOKEN y RAMPA_ORG en el entorno.

Modo legado (DEPRECATED): con --deploys-repo escribe apps/<app>/<scope>/values.yaml en un
checkout de rampa-deploys para que Argo CD lo aplique. No habla con la API.`,
		Example: `  rampa deploy --scope dev --image sha-a1b2c3d
  rampa deploy --scope prod --image sha-a1b2c3d --wait --timeout 20m
  rampa deploy --app checkout-api --scope pr-42 --image 123.dkr.ecr.us-east-1.amazonaws.com/rampa/checkout-api:sha-a1b2c3d --wait`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deploys != "" {
				warnDeprecated(cmd, "deploys-repo")
				return deployGitOps(cmd, file, scope, image, deploys, platform)
			}
			if platform != "" {
				return fmt.Errorf("--platform solo tiene sentido con --deploys-repo (modo legado); contra la API el cluster decide el dominio")
			}
			name, err := resolveAppName(appName, file)
			if err != nil {
				return err
			}
			s, err := newSession(org)
			if err != nil {
				return err
			}
			return deployAPI(cmd, s, name, scope, image, message, wait, timeout)
		},
	}
	addFileFlag(c, &file)
	c.Flags().StringVar(&appName, "app", "", "nombre de la application (default: metadata.name del rampa.yaml)")
	c.Flags().StringVarP(&scope, "scope", "s", "", "scope destino (dev, staging, prod, pr-<n>)")
	c.Flags().StringVar(&image, "image", "", "tag o referencia completa de la imagen")
	c.Flags().StringVarP(&message, "message", "m", "", "mensaje opcional del deployment")
	c.Flags().BoolVar(&wait, "wait", false, "esperar a que el deployment quede healthy (exit 1 si falla)")
	c.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "cuánto esperar con --wait")
	addOrgFlag(c, &org)
	c.Flags().StringVar(&deploys, "deploys-repo", "", "DEPRECATED: ruta local al checkout de rampa-deploys (modo GitOps legado, no usa la API)")
	c.Flags().StringVar(&platform, "platform", "", "DEPRECATED: ruta a platform.yaml (default: <deploys-repo>/platform.yaml)")
	_ = c.MarkFlagRequired("scope")
	_ = c.MarkFlagRequired("image")
	return c
}

// deployAPI crea el deployment por API y, con wait, sigue los eventos.
func deployAPI(cmd *cobra.Command, s *session, app, scope, image, message string, wait bool, timeout time.Duration) error {
	log := logger(cmd)
	out := cmd.OutOrStdout()

	ctx, cancel := requestCtx(cmd.Context())
	dep, err := s.client.CreateDeployment(ctx, s.org, app, scope, apiclient.CreateDeploymentRequest{Image: image, Message: message})
	cancel()
	if err != nil {
		if apiclient.IsNotFound(err) {
			return fmt.Errorf("creando el deployment de %s/%s en %s: %w\n  ¿Importaste la app? Probá `rampa apps import`. ¿El scope está atado a un cluster? Probá `rampa scope bind`.", app, scope, s.org, err)
		}
		return fmt.Errorf("creando el deployment de %s/%s en %s: %w", app, scope, s.org, err)
	}
	log.Debug("deployment creado", "id", dep.ID, "status", dep.Status)

	// La URL y el namespace vienen del scope, no del deployment. Best effort.
	namespace, url := app+"-"+scope, ""
	ctx, cancel = requestCtx(cmd.Context())
	scopes, err := s.client.ListScopes(ctx, s.org, app)
	cancel()
	if err != nil {
		log.Warn("no pude leer los scopes para resolver la URL", "err", err.Error())
	} else {
		for _, sc := range scopes {
			if sc.Name == scope {
				if sc.Namespace != "" {
					namespace = sc.Namespace
				}
				url = sc.URL
			}
		}
	}

	fmt.Fprintf(out, "✓ deployment %s creado (%s)\n", dep.ID, dep.Status)
	fmt.Fprintf(out, "  org: %s  app: %s  scope: %s  namespace: %s\n", s.org, app, scope, namespace)
	fmt.Fprintf(out, "  imagen: %s\n", dep.Image)
	if url != "" {
		fmt.Fprintf(out, "  url: %s\n", url)
	}
	if dep.Status == "awaiting_approval" {
		fmt.Fprintln(out, "\nEl scope requiere aprobación de un admin antes de aplicarse.")
	}

	final := dep
	if wait {
		fmt.Fprintf(out, "\nEsperando hasta %s a que el deployment quede healthy...\n", timeout)
		final, err = waitForDeployment(cmd, s, dep, timeout)
	}
	if werr := writeGitHubOutputs(map[string]string{
		"deployment_id": dep.ID, "status": final.Status, "namespace": namespace, "url": url, "image": dep.Image,
	}); werr != nil {
		return werr
	}
	if err != nil {
		return err
	}
	if wait {
		switch final.Status {
		case apiclient.DeploymentHealthy:
			fmt.Fprintf(out, "\n✓ %s/%s healthy con %s\n", app, scope, final.Image)
			if url != "" {
				fmt.Fprintf(out, "  url: %s\n", url)
			}
		default:
			msg := final.Message
			if msg == "" {
				msg = "sin detalle"
			}
			return fmt.Errorf("el deployment %s terminó en %s: %s", dep.ID, final.Status, msg)
		}
	}
	return nil
}

// waitForDeployment sigue el stream de eventos del deployment hasta un estado
// terminal o hasta agotar el timeout. Imprime cada evento. Complementa el
// stream con GETs periódicos para no perder un final que ya pasó.
func waitForDeployment(cmd *cobra.Command, s *session, dep *apiclient.Deployment, timeout time.Duration) (*apiclient.Deployment, error) {
	out := cmd.OutOrStdout()
	log := logger(cmd)
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	// check consulta el deployment por GET. parent es ctx durante la espera y el
	// contexto del comando una vez vencido el timeout.
	checkWith := func(parent context.Context) (*apiclient.Deployment, bool) {
		rctx, rcancel := requestCtx(parent)
		defer rcancel()
		d, err := s.client.GetDeployment(rctx, s.org, dep.ID)
		if err != nil {
			log.Warn("consultando el deployment", "err", err.Error())
			return nil, false
		}
		return d, d.IsTerminal()
	}
	check := func() (*apiclient.Deployment, bool) { return checkWith(ctx) }
	backoff := streamBackoffMin
	if d, done := check(); done {
		return d, nil
	}

	for {
		var terminal *apiclient.Event
		streamCtx, stopStream := context.WithCancel(ctx)
		streamErr := make(chan error, 1)
		go func() {
			streamErr <- s.client.StreamEvents(streamCtx, s.org, "deployment", dep.ID, func(ev apiclient.Event) error {
				if ev.SubjectType != "" && ev.SubjectType != "deployment" {
					return nil
				}
				if ev.SubjectID != "" && ev.SubjectID != dep.ID {
					return nil
				}
				printEvent(out, ev)
				switch ev.Kind {
				case apiclient.EventDeploymentHealthy, apiclient.EventDeploymentFailed, apiclient.EventDeploymentCancelled:
					e := ev
					terminal = &e
					return apiclient.ErrStopStream
				}
				return nil
			})
		}()

		ticker := time.NewTicker(deployPollInterval)
		var err error
	loop:
		for {
			select {
			case err = <-streamErr:
				break loop
			case <-ticker.C:
				if d, done := check(); done {
					stopStream()
					<-streamErr
					ticker.Stop()
					return d, nil
				}
			}
		}
		ticker.Stop()
		stopStream()

		if terminal != nil {
			// Confirmamos el estado final por GET; si falla, lo derivamos del evento.
			if d, done := check(); done {
				return d, nil
			}
			final := *dep
			final.Status = statusFromEvent(terminal.Kind)
			final.Message = terminal.Message
			return &final, nil
		}
		switch {
		case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
			// ctx ya venció: el GET final usa el contexto del comando, no el vencido.
			d, _ := checkWith(cmd.Context())
			if d == nil {
				d = dep
			}
			return d, fmt.Errorf("pasaron %s y el deployment %s sigue en %s; revisá `rampa scope list` o la web", timeout, dep.ID, d.Status)
		case errors.Is(err, io.EOF):
			log.Debug("el servidor cerró el stream, reconectando")
			backoff = streamBackoffMin
			if d, done := check(); done {
				return d, nil
			}
			select {
			case <-time.After(streamRetryDelay):
			case <-ctx.Done():
			}
		case err != nil && isFatalStreamError(err):
			// Antes de abortar, quizá el deployment ya terminó.
			if d, done := check(); done {
				return d, nil
			}
			return dep, fmt.Errorf("siguiendo los eventos del deployment %s: %w", dep.ID, err)
		case err != nil:
			// Error de transporte o 5xx: el deployment sigue su curso aunque el
			// stream se haya caído. Reconectamos con backoff hasta el timeout.
			log.Warn("se cayó el stream de eventos, reconectando", "err", err.Error(), "en", backoff.String())
			if d, done := check(); done {
				return d, nil
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
			}
			if backoff *= 2; backoff > streamBackoffMax {
				backoff = streamBackoffMax
			}
		default:
			// StreamEvents devolvió nil sin evento terminal: no debería pasar, reconectamos.
			log.Debug("stream terminado sin evento terminal, reconectando")
		}
	}
}

func statusFromEvent(kind string) string {
	switch kind {
	case apiclient.EventDeploymentHealthy:
		return apiclient.DeploymentHealthy
	case apiclient.EventDeploymentCancelled:
		return apiclient.DeploymentCancelled
	default:
		return apiclient.DeploymentFailed
	}
}

func printEvent(w io.Writer, ev apiclient.Event) {
	ts := ev.CreatedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	line := fmt.Sprintf("  %s  %-24s", ts.Local().Format("15:04:05"), ev.Kind)
	if ev.Message != "" {
		line += "  " + ev.Message
	}
	fmt.Fprintln(w, line)
}

// deployGitOps es el modo legado: escribe values.yaml en rampa-deploys.
func deployGitOps(cmd *cobra.Command, file, scope, image, deploys, platform string) error {
	app, err := manifest.LoadValid(file)
	if err != nil {
		return err
	}
	if platform == "" {
		platform = filepath.Join(deploys, "platform.yaml")
		if _, err := os.Stat(platform); err != nil {
			platform = ""
		}
	}
	plat, err := render.LoadPlatformOptional(platform)
	if err != nil {
		return err
	}
	vals, warns, err := render.Build(app, scope, render.Options{Image: image, Platform: plat})
	if err != nil {
		return err
	}
	for _, w := range warns {
		fmt.Fprintln(cmd.ErrOrStderr(), "aviso:", w)
	}

	dir := filepath.Join(deploys, "apps", app.Metadata.Name, scope)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Generado por `rampa deploy` desde el rampa.yaml de %s. No editar a mano.\n", app.Metadata.Name)
	body, err := yaml.Marshal(vals)
	if err != nil {
		return err
	}
	buf.Write(body)
	target := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(target, buf.Bytes(), 0o644); err != nil {
		return err
	}

	ns := app.Namespace(scope)
	url := render.URL(vals)
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "✓ %s escrito\n", target)
	fmt.Fprintf(out, "  app: %s  scope: %s  namespace: %s\n", app.Metadata.Name, scope, ns)
	fmt.Fprintf(out, "  imagen: %s:%s\n", vals.Image.Repository, vals.Image.Tag)
	if url != "" {
		fmt.Fprintf(out, "  url: %s\n", url)
	}
	if manifest.IsEphemeral(scope) || scope == "dev" {
		fmt.Fprintln(out, "\nCommiteá y pusheá rampa-deploys a main. Argo CD sincroniza solo.")
	} else {
		fmt.Fprintf(out, "\nAbrí un PR en rampa-deploys. Al mergear, Argo CD aplica la estrategia %s.\n", vals.Strategy)
	}
	return writeGitHubOutputs(map[string]string{
		"path": target, "namespace": ns, "url": url,
		"image": vals.Image.Repository + ":" + vals.Image.Tag,
	})
}

// writeGitHubOutputs publica outputs si corremos dentro de GitHub Actions.
func writeGitHubOutputs(kv map[string]string) error {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for k, v := range kv {
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, v); err != nil {
			return err
		}
	}
	return nil
}
