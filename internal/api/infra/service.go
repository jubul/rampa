package infra

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"rampa.dev/internal/api/auth"
	"rampa.dev/internal/api/crypto"
	"rampa.dev/internal/api/events"
	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/provider"
)

// Config es lo que el servicio necesita del entorno (docs/api.md).
type Config struct {
	// TofuBin es el binario de OpenTofu (nombre en PATH o ruta).
	TofuBin string
	// StateKey es la clave AES-256 del estado. Vacía deshabilita infra.
	StateKey []byte
	// Workdir es dónde se escriben los directorios de trabajo de los runs.
	Workdir string
	// Timeout es el máximo por run.
	Timeout time.Duration
	// Concurrency es cuántos runs corren a la vez en este proceso.
	Concurrency int
	// PublicURL es la URL pública de la API: va en api_url del agente y es la
	// base del backend http de estado.
	PublicURL string
	// AgentImage es la imagen del agente (repo:tag) para el chart.
	AgentImage string
}

// Parámetros del worker.
const (
	pollInterval = 5 * time.Second
	// LogTailLines es cuántas líneas trae log_tail en InfraRun.
	LogTailLines = 20
	// StateTokenPrefix identifica los tokens del backend de estado.
	StateTokenPrefix = "rampa_state_"
	// OrphanMessage es el error de los runs que un proceso anterior dejó a medias.
	OrphanMessage = "la API se reinició mientras el run estaba en ejecución"
	// StoppedMessage es el error de un run interrumpido por el apagado de la API.
	StoppedMessage = "la API se detuvo durante el run"
	// SubjectType es el subject_type de los eventos de un run.
	SubjectType = "infra_run"
	// BootstrapTokenTTL es cuánto vale el bootstrap token que el runner le pasa
	// al módulo: alcanza para un apply largo y no queda vigente para siempre en
	// el cloud-init de la máquina.
	BootstrapTokenTTL = 2 * time.Hour
)

// CredentialsFunc obtiene credenciales temporales para una cuenta. La
// implementación por defecto busca el provider registrado y exige que
// implemente provider.CredentialsProvider; los tests la reemplazan.
type CredentialsFunc func(ctx context.Context, acc provider.CloudAccount) (provider.Credentials, error)

// Service encola y ejecuta runs de infraestructura y sirve el backend http de estado.
type Service struct {
	cfg    Config
	st     *store.Store
	bus    *events.Bus
	log    *slog.Logger
	cipher *crypto.Cipher

	tofuPath string
	readyErr error
	worker   string
	wake     chan struct{}

	mu           sync.RWMutex
	stateBaseURL string
	credentials  CredentialsFunc
}

// New arma el servicio. No falla si falta tofu o la clave: deja Ready() con
// el motivo y los endpoints responden 501 hasta que se configure.
func New(cfg Config, st *store.Store, bus *events.Bus, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 40 * time.Minute
	}
	if cfg.TofuBin == "" {
		cfg.TofuBin = "tofu"
	}
	s := &Service{
		cfg:          cfg,
		st:           st,
		bus:          bus,
		log:          log,
		worker:       hostnamePid(),
		wake:         make(chan struct{}, 1),
		stateBaseURL: strings.TrimRight(cfg.PublicURL, "/"),
	}
	s.credentials = s.providerCredentials

	var problems []string
	if p, err := exec.LookPath(cfg.TofuBin); err != nil {
		problems = append(problems, fmt.Sprintf("no encuentro el binario de OpenTofu %q: instalalo (https://opentofu.org/docs/intro/install/) o apuntá RAMPA_TOFU_BIN a su ruta", cfg.TofuBin))
	} else {
		s.tofuPath = p
	}
	if len(cfg.StateKey) == 0 {
		problems = append(problems, "falta RAMPA_STATE_KEY (32 bytes en base64 o hex) para cifrar el estado de OpenTofu; generala con: openssl rand -base64 32")
	} else if c, err := crypto.NewCipher(cfg.StateKey); err != nil {
		problems = append(problems, "RAMPA_STATE_KEY inválida: "+err.Error())
	} else {
		s.cipher = c
	}
	if len(problems) > 0 {
		s.readyErr = errors.New("infraestructura con OpenTofu deshabilitada: " + strings.Join(problems, "; "))
	}
	return s
}

func hostnamePid() string {
	h, _ := os.Hostname()
	if h == "" {
		h = "api"
	}
	return fmt.Sprintf("%s-%d", h, os.Getpid())
}

// Ready devuelve nil si el runner puede ejecutar runs, o el motivo si no.
func (s *Service) Ready() error { return s.readyErr }

// Cipher devuelve el cifrado del servidor (RAMPA_STATE_KEY), o nil si no hay clave.
func (s *Service) Cipher() *crypto.Cipher { return s.cipher }

// AgentChartURL es la URL pública del chart rampa-agent empaquetado que
// reciben los módulos en agent_chart_url.
func (s *Service) AgentChartURL() string {
	return strings.TrimRight(s.cfg.PublicURL, "/") + AgentChartPath
}

// SetStateBaseURL cambia la base del backend http de estado (por defecto la
// URL pública). Los tests la apuntan al servidor de prueba.
func (s *Service) SetStateBaseURL(u string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateBaseURL = strings.TrimRight(u, "/")
}

// SetCredentialsFunc reemplaza cómo se obtienen las credenciales (tests).
func (s *Service) SetCredentialsFunc(f CredentialsFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credentials = f
}

func (s *Service) stateAddress(runID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stateBaseURL + "/v1/tfstate/" + runID
}

// Notify despierta al worker para que busque runs pendientes.
func (s *Service) Notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// providerCredentials es la CredentialsFunc por defecto.
func (s *Service) providerCredentials(ctx context.Context, acc provider.CloudAccount) (provider.Credentials, error) {
	p, ok := provider.Get(acc.Kind)
	if !ok {
		return provider.Credentials{}, fmt.Errorf("el provider %q no está registrado en esta instalación", acc.Kind)
	}
	cp, ok := p.(provider.CredentialsProvider)
	if !ok {
		return provider.Credentials{}, fmt.Errorf("el provider %s no puede emitir credenciales temporales para el runner (no implementa CredentialsProvider)", p.DisplayName())
	}
	return cp.Credentials(ctx, acc)
}

// ---- alta de runs ----

// ClusterRequest es lo que hace falta para crear un cluster (Kubernetes
// administrado o máquina con k3s) con OpenTofu.
type ClusterRequest struct {
	OrgID       string
	RequestedBy string
	Account     *store.CloudAccount
	// Module es la ruta del módulo ("digitalocean/droplet-k3s"). Vacío: el
	// módulo kind cluster del provider de la cuenta.
	Module       string
	Name         string
	Env          string
	Domain       string
	IngressClass string
	// SecretStore vacío toma el default del módulo (docs/api.md, tabla de módulos).
	SecretStore string
	// Inputs son los inputs propios del módulo, tal como llegan del formulario.
	// Se validan con ValidateInputs.
	Inputs map[string]any
	Actor  store.Actor
}

// ResolveModule devuelve el módulo de la request: el explícito, validado
// contra el provider de la cuenta, o el módulo cluster del provider.
func ResolveModule(providerKind, modulePath string) (Module, error) {
	if modulePath == "" {
		m, ok := FindModule(providerKind, KindCluster)
		if !ok || !m.Available {
			return Module{}, &InputError{Name: "module", Msg: fmt.Sprintf("todavía no hay módulo de cluster para el provider %s", providerKind)}
		}
		return m, nil
	}
	m, ok := FindModuleByPath(modulePath)
	if !ok {
		return Module{}, &InputError{Name: "module", Msg: fmt.Sprintf("módulo %q desconocido", modulePath)}
	}
	if m.Provider != providerKind {
		return Module{}, &InputError{Name: "module", Msg: fmt.Sprintf("el módulo %s es de %s y la cuenta es de %s", m.Module, m.Provider, providerKind)}
	}
	if !m.Available {
		return Module{}, &InputError{Name: "module", Msg: fmt.Sprintf("el módulo %s todavía no está disponible", m.Module)}
	}
	return m, nil
}

// CreateClusterRun crea el Cluster en Rampa (offline, con token, kind según el
// módulo) y encola el run que lo va a levantar, en una sola transacción. Los
// inputs guardados no llevan nada sensible: el bootstrap token (y el token del
// agente en aws/eks-cluster) se generan y se inyectan al correr.
func (s *Service) CreateClusterRun(ctx context.Context, req ClusterRequest) (*store.InfraRun, *store.Cluster, error) {
	m, err := ResolveModule(req.Account.Provider, req.Module)
	if err != nil {
		return nil, nil, err
	}
	typed, err := ValidateInputs(m, req.Inputs)
	if err != nil {
		return nil, nil, err
	}
	secretStore := req.SecretStore
	if secretStore == "" {
		secretStore = m.SecretStore
	}
	repo, tag := splitImage(s.cfg.AgentImage)
	inputs := map[string]any{
		"region":                 req.Account.Region,
		"name":                   req.Name,
		"env":                    req.Env,
		"api_url":                s.cfg.PublicURL,
		"agent_chart_url":        s.AgentChartURL(),
		"agent_image_repository": repo,
		"agent_image_tag":        tag,
	}
	if m.HasVariable("domain") {
		inputs["domain"] = req.Domain
	}
	if m.HasVariable("install_agent") {
		inputs["install_agent"] = true
	}
	for k, v := range typed {
		inputs[k] = v
	}
	inputsJSON, err := json.Marshal(inputs)
	if err != nil {
		return nil, nil, err
	}
	// El token del cluster se rota al ejecutar (o lo canjea el bootstrap);
	// este solo existe para que la fila tenga un hash válido.
	_, clusterHash, err := auth.NewClusterToken()
	if err != nil {
		return nil, nil, err
	}
	_, stateHash, err := newStateToken()
	if err != nil {
		return nil, nil, err
	}

	var run *store.InfraRun
	var cl *store.Cluster
	err = s.st.WithTx(ctx, func(tx *store.Store) error {
		var err error
		cl, err = tx.CreateCluster(ctx, &store.Cluster{
			OrgID: req.OrgID, CloudAccountID: req.Account.ID, Name: req.Name, Kind: m.ClusterKind(), Env: req.Env, TokenHash: clusterHash,
			Domain: req.Domain, IngressClass: req.IngressClass, SecretStore: secretStore,
		})
		if err != nil {
			return err
		}
		run, err = tx.CreateInfraRun(ctx, &store.InfraRun{
			OrgID: req.OrgID, Kind: m.Kind, Provider: req.Account.Provider, Module: m.Module,
			CloudAccountID: req.Account.ID, ClusterID: cl.ID, Name: req.Name, Operation: store.InfraOpApply,
			Inputs: inputsJSON, StateTokenHash: stateHash, RequestedBy: req.RequestedBy,
		})
		if err != nil {
			return err
		}
		if err := tx.SetClusterInfraRun(ctx, req.OrgID, cl.ID, run.ID); err != nil {
			return err
		}
		cl.InfraRunID = run.ID
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	s.bus.Emit(ctx, req.OrgID, req.Actor, "cluster.created", "cluster", cl.ID, "cluster "+cl.Name+" ("+cl.Env+", "+cl.Kind+") creado; OpenTofu lo va a levantar con "+m.Module, map[string]string{"name": cl.Name, "kind": cl.Kind, "env": cl.Env, "domain": cl.Domain, "infra_run_id": run.ID, "module": m.Module})
	s.emitStatus(ctx, run, req.Actor)
	s.Notify()
	return run, cl, nil
}

// Destroy encola tofu destroy sobre un run terminado (succeeded o failed).
func (s *Service) Destroy(ctx context.Context, orgID, runID string, actor store.Actor) (*store.InfraRun, error) {
	run, err := s.st.RequeueInfraRun(ctx, orgID, runID, store.InfraOpDestroy, store.InfraRunDestroying, []string{store.InfraRunSucceeded, store.InfraRunFailed})
	if err != nil {
		return nil, err
	}
	s.emitStatus(ctx, run, actor)
	s.Notify()
	return run, nil
}

// Retry vuelve a encolar un run failed con su misma operación.
func (s *Service) Retry(ctx context.Context, orgID, runID string, actor store.Actor) (*store.InfraRun, error) {
	current, err := s.st.GetInfraRun(ctx, orgID, runID)
	if err != nil {
		return nil, err
	}
	status := store.InfraRunQueued
	if current.Operation == store.InfraOpDestroy {
		status = store.InfraRunDestroying
	}
	run, err := s.st.RequeueInfraRun(ctx, orgID, runID, current.Operation, status, []string{store.InfraRunFailed})
	if err != nil {
		return nil, err
	}
	s.emitStatus(ctx, run, actor)
	s.Notify()
	return run, nil
}

// ---- worker ----

// Run corre el worker hasta que ctx se cancela: recupera los runs huérfanos,
// toma los pendientes (uno por cuenta de nube, hasta Concurrency a la vez) y
// los ejecuta. Si el runner no está listo, solo hace la recuperación.
func (s *Service) Run(ctx context.Context) {
	s.Recover(ctx)
	if err := s.Ready(); err != nil {
		s.log.Warn(err.Error())
		<-ctx.Done()
		return
	}
	s.log.Info("runner de infraestructura listo", "tofu", s.tofuPath, "concurrency", s.cfg.Concurrency, "timeout", s.cfg.Timeout, "workdir", s.cfg.Workdir)
	sem := make(chan struct{}, s.cfg.Concurrency)
	var wg sync.WaitGroup
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		s.claim(ctx, sem, &wg)
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

// claim toma runs mientras haya capacidad y trabajo.
func (s *Service) claim(ctx context.Context, sem chan struct{}, wg *sync.WaitGroup) {
	for {
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		default:
			return // sin capacidad
		}
		run, err := s.st.ClaimInfraRun(ctx, s.worker)
		if err != nil {
			<-sem
			if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
				s.log.Error("tomando un run de infraestructura", "err", err)
			}
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.execute(ctx, run)
			// Puede haber otro run de la misma cuenta esperando.
			s.Notify()
		}()
	}
}

// Recover cierra como failed los runs que quedaron tomados por un proceso
// anterior y deja sus clusters en failed.
func (s *Service) Recover(ctx context.Context) {
	runs, err := s.st.FailOrphanInfraRuns(ctx, OrphanMessage)
	if err != nil {
		s.log.Error("recuperando runs huérfanos", "err", err)
		return
	}
	for i := range runs {
		run := &runs[i]
		s.log.Warn("run de infraestructura huérfano marcado como failed", "run_id", run.ID, "org_id", run.OrgID)
		actor := store.Actor{Type: "system", Name: "runner"}
		s.afterFailure(ctx, run, actor)
		s.emitStatus(ctx, run, actor)
	}
}

// execute corre un run tomado de la cola, de punta a punta.
func (s *Service) execute(parent context.Context, run *store.InfraRun) {
	actor := store.Actor{Type: "system", Name: "runner"}
	// Las escrituras finales no dependen del ctx del worker: si la API se
	// apaga a mitad de camino, igual queda registrado el failed.
	bg := context.WithoutCancel(parent)
	ctx, cancel := context.WithTimeout(parent, s.cfg.Timeout)
	defer cancel()

	stateToken, stateHash, err := newStateToken()
	if err != nil {
		s.finish(bg, run, actor, store.InfraRunFailed, err.Error(), nil)
		return
	}
	initial := store.InfraRunInitializing
	if run.Operation == store.InfraOpDestroy {
		initial = store.InfraRunDestroying
	}
	run, err = s.st.StartInfraRun(bg, run.OrgID, run.ID, initial, stateHash)
	if err != nil {
		s.log.Error("iniciando el run", "run_id", run.ID, "err", err)
		return
	}
	s.emitStatus(bg, run, actor)

	lg, err := newRunLogger(bg, s, run)
	if err != nil {
		s.finish(bg, run, actor, store.InfraRunFailed, "no pude abrir el log del run: "+err.Error(), nil)
		return
	}
	lg.red.add(stateToken)
	lg.line(fmt.Sprintf("== rampa: tofu %s de %s (intento %d) ==", run.Operation, run.Module, run.Attempt))

	outputs, runErr := s.steps(ctx, run, stateToken, lg, actor)
	switch {
	case runErr == nil && run.Operation == store.InfraOpDestroy:
		lg.line("== rampa: destroy completo ==")
		s.finish(bg, run, actor, store.InfraRunDestroyed, "", nil)
	case runErr == nil:
		lg.line("== rampa: apply completo ==")
		s.finish(bg, run, actor, store.InfraRunSucceeded, "", outputs)
	default:
		msg := lg.red.redact(runErr.Error())
		if parent.Err() != nil {
			msg = StoppedMessage
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			msg = fmt.Sprintf("el run superó el tiempo máximo (%s)", s.cfg.Timeout)
		}
		lg.line("== rampa: error: " + msg + " ==")
		s.finish(bg, run, actor, store.InfraRunFailed, msg, nil)
	}
}

// steps ejecuta init, plan y apply (o destroy) y lee los outputs.
func (s *Service) steps(ctx context.Context, run *store.InfraRun, stateToken string, lg *runLogger, actor store.Actor) (json.RawMessage, error) {
	m, ok := FindModuleByPath(run.Module)
	if !ok || !m.Available {
		return nil, fmt.Errorf("el módulo %s no está disponible en esta versión de la API", run.Module)
	}
	acc, err := s.st.GetCloudAccount(ctx, run.OrgID, run.CloudAccountID)
	if err != nil {
		return nil, fmt.Errorf("cargando la cuenta de nube: %w", err)
	}
	if acc.Status != store.CloudAccountConnected {
		return nil, fmt.Errorf("la cuenta %s no está conectada (status %s); verificala antes de correr infraestructura", acc.Name, acc.Status)
	}

	wd, err := WriteWorkdir(s.cfg.Workdir, m)
	if err != nil {
		return nil, err
	}
	defer wd.Remove()

	var inputs map[string]any
	if err := json.Unmarshal(run.Inputs, &inputs); err != nil {
		return nil, fmt.Errorf("inputs del run corruptos: %w", err)
	}
	if inputs == nil {
		inputs = map[string]any{}
	}
	if m.HasVariable("agent_chart_path") {
		inputs["agent_chart_path"] = wd.AgentChartPath
	}
	// Solo las variables que el root declara: un input viejo no rompe el run.
	for k := range inputs {
		if !m.HasVariable(k) {
			delete(inputs, k)
		}
	}
	if err := wd.WriteTFVars(inputs); err != nil {
		return nil, fmt.Errorf("escribiendo %s: %w", TFVarsFile, err)
	}
	lg.line("directorio de trabajo preparado: módulo " + m.Module + " y chart rampa-agent")

	pacc, err := s.providerAccount(ctx, acc)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	credsFn := s.credentials
	s.mu.RUnlock()
	creds, err := credsFn(provider.WithRunID(ctx, run.ID), pacc)
	if err != nil {
		return nil, fmt.Errorf("obteniendo credenciales de la cuenta %s: %w", acc.Name, err)
	}
	if creds.Expires.IsZero() {
		lg.line(fmt.Sprintf("credenciales de la cuenta %s cargadas (token de larga duración, cifrado en Rampa)", acc.Name))
	} else {
		lg.line(fmt.Sprintf("credenciales temporales obtenidas para la cuenta %s (vencen %s)", acc.Name, creds.Expires.UTC().Format(time.RFC3339)))
	}

	// Los valores secretos que el subproceso podría imprimir no llegan al log.
	lg.red.add(stateToken, creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken)
	for _, v := range creds.Env {
		lg.red.add(v)
	}
	env := s.subprocessEnv(run, wd, stateToken, creds, acc)
	if run.Operation == store.InfraOpApply && run.CreatesCluster() {
		// El agente se instala con un bootstrap token de un solo uso que solo
		// vive en el entorno del subproceso (TF_VAR_bootstrap_token); el módulo
		// lo pone en el cloud-init o en el chart y el agente lo canjea por el
		// token real en POST /v1/agent/bootstrap. En la base queda solo el hash.
		plain, hash, err := auth.NewBootstrapToken()
		if err != nil {
			return nil, err
		}
		if _, err := s.st.CreateBootstrapToken(ctx, run.OrgID, run.ClusterID, hash, time.Now().Add(BootstrapTokenTTL), run.RequestedBy); err != nil {
			return nil, fmt.Errorf("creando el bootstrap token del cluster: %w", err)
		}
		lg.red.add(plain)
		env = append(env, "TF_VAR_bootstrap_token="+plain)
		lg.line(fmt.Sprintf("bootstrap token del agente generado (vence en %s); el agente lo canjea al arrancar", BootstrapTokenTTL))
		if m.HasVariable("agent_token") {
			// aws/eks-cluster sigue aceptando el token del cluster para instalar el
			// agente con Helm en el mismo apply: se rota y solo el hash queda.
			plain, hash, err := auth.NewClusterToken()
			if err != nil {
				return nil, err
			}
			if _, err := s.st.RotateClusterToken(ctx, run.OrgID, run.ClusterID, hash); err != nil {
				return nil, fmt.Errorf("rotando el token del cluster: %w", err)
			}
			lg.red.add(plain)
			env = append(env, "TF_VAR_agent_token="+plain)
			lg.line("token del agente generado; se instala con Helm dentro del apply")
		}
	}

	tofu := func(sub string, extra ...string) error {
		args := append([]string{sub, "-no-color", "-input=false"}, extra...)
		return s.runTofu(ctx, wd.Dir, env, lg, args...)
	}
	if err := tofu("init"); err != nil {
		return nil, err
	}
	if run.Operation == store.InfraOpDestroy {
		if err := tofu("destroy", "-auto-approve"); err != nil {
			return nil, err
		}
		return nil, nil
	}
	s.transition(ctx, run, store.InfraRunPlanning, actor)
	if err := tofu("plan", "-out=tfplan"); err != nil {
		return nil, err
	}
	s.transition(ctx, run, store.InfraRunApplying, actor)
	if err := tofu("apply", "tfplan"); err != nil {
		return nil, err
	}
	outputs, err := s.readOutputs(ctx, wd.Dir, env)
	if err != nil {
		return nil, err
	}
	return outputs, nil
}

// subprocessEnv arma el entorno del subproceso: nada del entorno de la API
// salvo PATH, más el backend http, la automatización y las credenciales de la
// cuenta. Las credenciales no se escriben a disco ni al log.
func (s *Service) subprocessEnv(run *store.InfraRun, wd *Workdir, stateToken string, creds provider.Credentials, acc *store.CloudAccount) []string {
	addr := s.stateAddress(run.ID)
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + wd.Dir,
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"TF_PLUGIN_CACHE_DIR=" + s.pluginCacheDir(),
		"TF_HTTP_ADDRESS=" + addr,
		"TF_HTTP_LOCK_ADDRESS=" + addr,
		"TF_HTTP_UNLOCK_ADDRESS=" + addr,
		"TF_HTTP_LOCK_METHOD=LOCK",
		"TF_HTTP_UNLOCK_METHOD=UNLOCK",
		"TF_HTTP_USERNAME=rampa",
		"TF_HTTP_PASSWORD=" + stateToken,
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	if creds.AccessKeyID != "" {
		env = append(env, "AWS_ACCESS_KEY_ID="+creds.AccessKeyID)
	}
	if creds.SecretAccessKey != "" {
		env = append(env, "AWS_SECRET_ACCESS_KEY="+creds.SecretAccessKey)
	}
	if creds.SessionToken != "" {
		env = append(env, "AWS_SESSION_TOKEN="+creds.SessionToken)
	}
	if acc.Provider == string(provider.AWS) && acc.Region != "" {
		env = append(env, "AWS_REGION="+acc.Region, "AWS_DEFAULT_REGION="+acc.Region)
	}
	for k, v := range creds.Env {
		env = append(env, k+"="+v)
	}
	return env
}

// pluginCacheDir es un cache de providers compartido entre runs, para no
// bajar el provider de AWS en cada apply.
func (s *Service) pluginCacheDir() string {
	base := s.cfg.Workdir
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "rampa-tofu-plugin-cache")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// runTofu ejecuta tofu con args en dir, volcando stdout y stderr al log línea
// por línea. El error resume el código de salida y las últimas líneas de stderr.
func (s *Service) runTofu(ctx context.Context, dir string, env []string, lg *runLogger, args ...string) error {
	lg.line("$ tofu " + strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, s.tofuPath, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("no pude ejecutar %s: %w", s.tofuPath, err)
	}
	var wg sync.WaitGroup
	var tail tailBuffer
	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			lg.line(sc.Text())
		}
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			line = lg.red.redact(line)
			tail.add(line)
			lg.line(line)
		}
	}()
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := fmt.Sprintf("tofu %s falló", args[0])
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			msg += fmt.Sprintf(" (código %d)", exitErr.ExitCode())
		} else {
			msg += ": " + err.Error()
		}
		if t := tail.String(); t != "" {
			msg += ": " + t
		}
		if looksLikeAccessDenied(tail.String()) {
			msg += ". Parece un problema de permisos en la cuenta: actualizá el stack RampaConnect con ManageInfrastructure=true"
		}
		return errors.New(msg)
	}
	return nil
}

// readOutputs corre tofu output -json y lo reduce a {nombre: valor}. No va al
// log: los outputs pueden ser sensibles.
func (s *Service) readOutputs(ctx context.Context, dir string, env []string) (json.RawMessage, error) {
	cmd := exec.CommandContext(ctx, s.tofuPath, "output", "-no-color", "-json")
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tofu output -json falló: %w", err)
	}
	var raw map[string]struct {
		Value     json.RawMessage `json:"value"`
		Sensitive bool            `json:"sensitive"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("tofu output -json devolvió algo que no entiendo: %w", err)
	}
	values := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		if v.Sensitive {
			continue
		}
		values[k] = v.Value
	}
	return json.Marshal(values)
}

// transition cambia el estado de un run en curso y lo anuncia.
func (s *Service) transition(ctx context.Context, run *store.InfraRun, status string, actor store.Actor) {
	if err := s.st.SetInfraRunStatus(context.WithoutCancel(ctx), run.OrgID, run.ID, status); err != nil {
		s.log.Error("cambiando el estado del run", "run_id", run.ID, "status", status, "err", err)
		return
	}
	run.Status = status
	s.emitStatus(ctx, run, actor)
}

// finish aplica los efectos sobre el cluster y después cierra el run: cuando
// un cliente ve el estado terminal, el cluster ya refleja el resultado.
func (s *Service) finish(ctx context.Context, run *store.InfraRun, actor store.Actor, status, errMsg string, outputs json.RawMessage) {
	run.Status = status
	run.Error = errMsg
	if len(outputs) > 0 {
		run.Outputs = outputs
	}
	switch status {
	case store.InfraRunSucceeded:
		s.afterSuccess(ctx, run, actor)
	case store.InfraRunDestroyed:
		s.afterDestroy(ctx, run, actor)
	case store.InfraRunFailed:
		s.afterFailure(ctx, run, actor)
	}
	done, err := s.st.FinishInfraRun(ctx, run.OrgID, run.ID, status, errMsg, outputs)
	if err != nil {
		s.log.Error("cerrando el run", "run_id", run.ID, "status", status, "err", err)
		return
	}
	s.log.Info("run de infraestructura terminado", "run_id", done.ID, "org_id", done.OrgID, "status", done.Status, "operation", done.Operation)
	s.emitStatus(ctx, done, actor)
}

func (s *Service) afterSuccess(ctx context.Context, run *store.InfraRun, actor store.Actor) {
	if !run.CreatesCluster() {
		return
	}
	var outputs map[string]json.RawMessage
	_ = json.Unmarshal(run.Outputs, &outputs)
	var roleARN string
	if raw, ok := outputs["agent_role_arn"]; ok {
		_ = json.Unmarshal(raw, &roleARN)
	}
	if err := s.st.SetClusterInfraResult(ctx, run.OrgID, run.ClusterID, roleARN, store.ClusterOffline); err != nil {
		s.log.Error("actualizando el cluster tras el apply", "cluster_id", run.ClusterID, "err", err)
		return
	}
	// Una máquina creada sin dominio recibe <ip>.sslip.io: resuelve solo a esa IP
	// y da URLs que funcionan sin configurar DNS. Solo si el cluster no tenía dominio.
	var domain string
	if raw, ok := outputs["public_ip"]; ok {
		var ip string
		_ = json.Unmarshal(raw, &ip)
		if d := SslipDomain(ip); d != "" {
			if set, err := s.st.SetClusterDomain(ctx, run.OrgID, run.ClusterID, d); err != nil {
				s.log.Error("fijando el dominio sslip del cluster", "cluster_id", run.ClusterID, "err", err)
			} else if set {
				domain = d
			}
		}
	}
	msg := fmt.Sprintf("la infraestructura del cluster %s está lista; el agente se está conectando", run.Name)
	if domain != "" {
		msg += fmt.Sprintf("; las apps van a publicarse en *.%s", domain)
	}
	s.bus.Emit(ctx, run.OrgID, actor, "cluster.infra_ready", "cluster", run.ClusterID, msg,
		map[string]any{"run_id": run.ID, "name": run.Name, "agent_role_arn": roleARN, "kind": run.Kind, "domain": domain})
}

func (s *Service) afterFailure(ctx context.Context, run *store.InfraRun, actor store.Actor) {
	if !run.CreatesCluster() {
		return
	}
	if err := s.st.SetClusterInfraResult(ctx, run.OrgID, run.ClusterID, "", store.ClusterFailed); err != nil {
		s.log.Error("marcando el cluster como failed", "cluster_id", run.ClusterID, "err", err)
	}
}

func (s *Service) afterDestroy(ctx context.Context, run *store.InfraRun, actor store.Actor) {
	if !run.CreatesCluster() {
		return
	}
	err := s.st.DeleteCluster(ctx, run.OrgID, run.ClusterID)
	switch {
	case err == nil:
		s.bus.Emit(ctx, run.OrgID, actor, "cluster.deleted", "cluster", run.ClusterID, "cluster "+run.Name+" destruido y borrado de Rampa", map[string]string{"run_id": run.ID, "name": run.Name})
	case errors.Is(err, store.ErrConflict):
		s.log.Warn("el cluster destruido tiene scopes; no se borra de Rampa", "cluster_id", run.ClusterID)
		if err := s.st.SetClusterInfraResult(ctx, run.OrgID, run.ClusterID, "", store.ClusterFailed); err != nil {
			s.log.Error("marcando el cluster destruido", "cluster_id", run.ClusterID, "err", err)
		}
	case errors.Is(err, store.ErrNotFound):
	default:
		s.log.Error("borrando el cluster tras el destroy", "cluster_id", run.ClusterID, "err", err)
	}
}

// emitStatus emite infra_run.status con el estado actual del run.
func (s *Service) emitStatus(ctx context.Context, run *store.InfraRun, actor store.Actor) {
	s.bus.Emit(ctx, run.OrgID, actor, "infra_run.status", SubjectType, run.ID,
		fmt.Sprintf("run %s de %s: %s", run.Operation, run.Name, run.Status),
		map[string]any{"run_id": run.ID, "status": run.Status, "kind": run.Kind, "operation": run.Operation, "cluster_id": run.ClusterID, "name": run.Name, "error": run.Error})
}

// ---- log por líneas ----

// runLogger numera las líneas del log, las persiste y las emite en vivo.
type runLogger struct {
	ctx context.Context
	s   *Service
	run *store.InfraRun
	mu  sync.Mutex
	seq int
	// red oculta los secretos conocidos del run en cada línea.
	red redactor
}

func newRunLogger(ctx context.Context, s *Service, run *store.InfraRun) (*runLogger, error) {
	seq, err := s.st.MaxInfraRunLogSeq(ctx, run.OrgID, run.ID)
	if err != nil {
		return nil, err
	}
	return &runLogger{ctx: ctx, s: s, run: run, seq: seq}, nil
}

// line agrega una línea. Errores de persistencia se loguean y no frenan el run.
func (l *runLogger) line(text string) {
	text = l.red.redact(text)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	seq := l.seq
	if err := l.s.st.AppendInfraRunLog(l.ctx, l.run.OrgID, l.run.ID, seq, text); err != nil {
		l.s.log.Warn("no pude guardar una línea del log", "run_id", l.run.ID, "err", err)
	}
	l.s.bus.Broadcast(l.run.OrgID, store.Actor{Type: "system", Name: "runner"}, "infra_run.line", SubjectType, l.run.ID, "",
		map[string]any{"run_id": l.run.ID, "seq": seq, "line": text})
}

// tailBuffer guarda las últimas líneas de stderr para el mensaje de error.
type tailBuffer struct {
	mu    sync.Mutex
	lines []string
}

const tailLines = 6

func (t *tailBuffer) add(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > tailLines {
		t.lines = t.lines[len(t.lines)-tailLines:]
	}
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.Join(t.lines, " | ")
	if len(s) > 1500 {
		s = s[:1500] + "..."
	}
	return s
}

func looksLikeAccessDenied(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "accessdenied") || strings.Contains(l, "unauthorizedoperation") || strings.Contains(l, "not authorized")
}

// newStateToken genera el token del backend de estado y su hash.
func newStateToken() (plain, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generando el token de estado: %w", err)
	}
	plain = StateTokenPrefix + hex.EncodeToString(b)
	return plain, auth.HashToken(plain), nil
}

func toProviderAccount(acc *store.CloudAccount) provider.CloudAccount {
	settings := map[string]string{}
	for k, v := range acc.Settings {
		settings[k] = v
	}
	return provider.CloudAccount{
		ID: acc.ID, OrgID: acc.OrgID, Kind: provider.Kind(acc.Provider), Name: acc.Name,
		ExternalRef: acc.ExternalRef, Region: acc.Region, Settings: settings,
	}
}

// providerAccount arma la cuenta para el provider y, si tiene credenciales
// cifradas (providers con auth token), las descifra en memoria. Nunca se
// loguean ni se escriben a disco.
func (s *Service) providerAccount(ctx context.Context, acc *store.CloudAccount) (provider.CloudAccount, error) {
	pacc := toProviderAccount(acc)
	if !acc.HasCredentials {
		return pacc, nil
	}
	if s.cipher == nil {
		return pacc, fmt.Errorf("la cuenta %s tiene credenciales cifradas pero falta RAMPA_STATE_KEY para descifrarlas", acc.Name)
	}
	ct, err := s.st.GetCloudAccountCredentials(ctx, acc.OrgID, acc.ID)
	if err != nil {
		return pacc, fmt.Errorf("leyendo las credenciales de la cuenta %s: %w", acc.Name, err)
	}
	creds, err := s.cipher.OpenCredentials(acc.ID, ct)
	if err != nil {
		return pacc, err
	}
	pacc.Credentials = creds
	return pacc, nil
}

// splitImage separa "repo:tag". Un ":" dentro del host (puerto) no cuenta.
func splitImage(image string) (repo, tag string) {
	image = strings.TrimSpace(image)
	if image == "" {
		return "", ""
	}
	i := strings.LastIndex(image, ":")
	if i < 0 || strings.Contains(image[i+1:], "/") {
		return image, ""
	}
	return image[:i], image[i+1:]
}

// SslipDomain arma el dominio comodín de sslip.io para una IPv4 pública:
// 1.2.3.4 -> 1-2-3-4.sslip.io, así *.1-2-3-4.sslip.io resuelve a 1.2.3.4.
// Devuelve "" si ip no es una IPv4 válida.
func SslipDomain(ip string) string {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil || parsed.To4() == nil {
		return ""
	}
	return strings.ReplaceAll(parsed.To4().String(), ".", "-") + ".sslip.io"
}
