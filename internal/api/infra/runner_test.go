package infra

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"rampa.dev/internal/api/store"
	"rampa.dev/pkg/provider"
	_ "rampa.dev/pkg/provider/digitalocean"
)

// Credenciales falsas: los tests verifican que nunca lleguen al log.
const (
	fakeAccessKey = "AKIAFAKEKEYFORTESTS0"
	fakeSecret    = "secretFAKE/verySecret+Value"
	fakeSession   = "sessionFAKEtokenXYZ"
)

// runnerEnv es el servicio con el tofu falso, el backend de estado servido por
// httptest y una suscripción al bus que acumula eventos de la org.
type runnerEnv struct {
	f   *fixture
	svc *Service

	mu     sync.Mutex
	events []*store.Event
}

func newRunnerEnv(t *testing.T) *runnerEnv {
	t.Helper()
	f := newFixture(t)
	svc := New(Config{
		TofuBin: fakeTofu(t), StateKey: testKey(3), Workdir: t.TempDir(), Timeout: time.Minute, Concurrency: 2,
		PublicURL: "http://api.test.local", AgentImage: "ghcr.io/acme/rampa-agent:9.9",
	}, f.st, f.bus, quietLog())
	if err := svc.Ready(); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Mount("/v1/tfstate", svc.StateHandler())
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	svc.SetStateBaseURL(ts.URL)
	svc.SetCredentialsFunc(func(ctx context.Context, acc provider.CloudAccount) (provider.Credentials, error) {
		if provider.RunIDFrom(ctx) == "" {
			return provider.Credentials{}, errors.New("el runner tiene que anotar el run id en el contexto")
		}
		return provider.Credentials{AccessKeyID: fakeAccessKey, SecretAccessKey: fakeSecret, SessionToken: fakeSession, Expires: time.Now().Add(time.Hour), Env: map[string]string{"AWS_EXTRA": "x"}}, nil
	})
	e := &runnerEnv{f: f, svc: svc}
	sub := f.bus.Subscribe(f.orgID, store.EventFilter{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-sub.C:
				e.mu.Lock()
				e.events = append(e.events, ev)
				e.mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() { cancel(); f.bus.Unsubscribe(sub) })
	return e
}

func (e *runnerEnv) startWorker() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	e.f.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			e.f.t.Error("el worker no terminó")
		}
	})
}

func (e *runnerEnv) run(id string) *store.InfraRun {
	r, err := e.f.st.GetInfraRun(context.Background(), e.f.orgID, id)
	if err != nil {
		e.f.t.Fatal(err)
	}
	return r
}

func (e *runnerEnv) waitStatus(id, want string) *store.InfraRun {
	e.f.t.Helper()
	var last *store.InfraRun
	waitFor(e.f.t, "run "+id+" en "+want, 30*time.Second, func() bool {
		last = e.run(id)
		return last.Status == want
	})
	return last
}

func (e *runnerEnv) logText(id string) string {
	lines, err := e.f.st.ListInfraRunLog(context.Background(), e.f.orgID, id, 0)
	if err != nil {
		e.f.t.Fatal(err)
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Line)
		b.WriteString("\n")
	}
	return b.String()
}

func (e *runnerEnv) eventKinds(subjectID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.events {
		if ev.SubjectID == subjectID {
			out = append(out, ev.Kind)
		}
	}
	return out
}

func (e *runnerEnv) statusSequence(runID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.events {
		if ev.SubjectID == runID && ev.Kind == "infra_run.status" {
			var p struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(ev.Payload, &p)
			out = append(out, p.Status)
		}
	}
	return out
}

func (e *runnerEnv) createCluster(name string) (*store.InfraRun, *store.Cluster) {
	e.f.t.Helper()
	run, cl, err := e.svc.CreateClusterRun(context.Background(), ClusterRequest{
		OrgID: e.f.orgID, RequestedBy: e.f.user.ID, Account: e.f.acc, Name: name, Env: "nonprod", Domain: "apps.dev.acme.com",
		IngressClass: "nginx", SecretStore: "aws-secrets-manager",
		Inputs: map[string]any{"kubernetes_version": "1.33", "admin_principal_arns": []string{"arn:aws:iam::123456789012:user/juan"}},
		Actor:  store.Actor{Type: "user", ID: e.f.user.ID, Name: "Infra"},
	})
	if err != nil {
		e.f.t.Fatal(err)
	}
	return run, cl
}

func TestRunnerApplyThenDestroy(t *testing.T) {
	e := newRunnerEnv(t)
	run, cl := e.createCluster("rampa-nonprod")
	ctx := context.Background()

	if run.Status != store.InfraRunQueued || run.Operation != store.InfraOpApply || run.ClusterID != cl.ID || cl.InfraRunID != run.ID || cl.Status != store.ClusterOffline {
		t.Fatalf("run/cluster iniciales: %+v %+v", run, cl)
	}
	var inputs map[string]any
	_ = json.Unmarshal(run.Inputs, &inputs)
	if inputs["api_url"] != "http://api.test.local" || inputs["agent_image_repository"] != "ghcr.io/acme/rampa-agent" || inputs["agent_image_tag"] != "9.9" || inputs["region"] != "us-east-1" || inputs["install_agent"] != true || inputs["agent_chart_url"] != "http://api.test.local/public/charts/rampa-agent.tgz" || inputs["vpc_cidr"] != DefaultVPCCIDR {
		t.Errorf("inputs: %v", inputs)
	}
	if _, has := inputs["agent_token"]; has || strings.Contains(string(run.Inputs), "rampa_cluster_") {
		t.Errorf("inputs no debe llevar el token: %s", run.Inputs)
	}
	if _, has := inputs["bootstrap_token"]; has || strings.Contains(string(run.Inputs), "rampa_bootstrap_") {
		t.Errorf("inputs no debe llevar el bootstrap token: %s", run.Inputs)
	}
	if cl.Kind != store.ClusterKindKubernetes || cl.SecretStore != "aws-secrets-manager" {
		t.Errorf("cluster: %+v", cl)
	}
	tokenBefore := cl.TokenHash

	e.startWorker()
	done := e.waitStatus(run.ID, store.InfraRunSucceeded)
	if done.Error != "" || done.StartedAt == nil || done.FinishedAt == nil || done.Attempt != 1 {
		t.Errorf("run terminado: %+v", done)
	}
	var outputs map[string]json.RawMessage
	if err := json.Unmarshal(done.Outputs, &outputs); err != nil {
		t.Fatal(err)
	}
	var roleARN string
	var subnets []string
	_ = json.Unmarshal(outputs["agent_role_arn"], &roleARN)
	_ = json.Unmarshal(outputs["private_subnet_ids"], &subnets)
	if roleARN != "arn:aws:iam::123456789012:role/rampa-nonprod-rampa-agent" || len(subnets) != 2 || subnets[1] != "subnet-b" {
		t.Errorf("outputs: %s", done.Outputs)
	}
	if _, leaked := outputs["secreto"]; leaked {
		t.Error("un output sensible no debe guardarse")
	}

	// El cluster tiene el rol del agente, sigue offline (el agente aún no conectó) y su token rotó.
	clAfter, err := e.f.st.GetCluster(ctx, e.f.orgID, cl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if clAfter.AgentRoleARN != "arn:aws:iam::123456789012:role/rampa-nonprod-rampa-agent" || clAfter.Status != store.ClusterOffline || clAfter.TokenHash == tokenBefore {
		t.Errorf("cluster tras el apply: %+v", clAfter)
	}

	// Log: pasos, líneas de stdout y stderr, y ninguna credencial.
	log := e.logText(run.ID)
	for _, want := range []string{"$ tofu init -no-color -input=false", "$ tofu plan -no-color -input=false -out=tfplan", "$ tofu apply -no-color -input=false tfplan", "OpenTofu has been successfully initialized!", "Plan: 12 to add", "Apply complete!", "credenciales presentes: AWS_ACCESS_KEY_ID=si AWS_REGION=us-east-1", "charts/rampa-agent", "== rampa: apply completo =="} {
		if !strings.Contains(log, want) {
			t.Errorf("falta %q en el log:\n%s", want, log)
		}
	}
	for _, secret := range []string{fakeAccessKey, fakeSecret, fakeSession, "rampa_cluster_", "rampa_state_", "rampa_bootstrap_"} {
		if strings.Contains(log, secret) {
			t.Errorf("el log contiene %q:\n%s", secret, log)
		}
	}
	if !strings.Contains(log, "bootstrap_token presente: si") || !strings.Contains(log, "bootstrap token del agente generado") {
		t.Errorf("el módulo debería recibir TF_VAR_bootstrap_token:\n%s", log)
	}
	// Quedó un bootstrap token sin usar para el cluster, con vencimiento a 2 horas.
	bts, err := e.f.st.ListBootstrapTokens(ctx, e.f.orgID, cl.ID)
	if err != nil || len(bts) != 1 || bts[0].UsedAt != nil || time.Until(bts[0].ExpiresAt) < 90*time.Minute {
		t.Errorf("bootstrap tokens: %v %+v", err, bts)
	}
	if strings.Contains(log, "no-deberia-salir") {
		t.Error("los outputs no van al log")
	}
	tail, _ := e.f.st.InfraRunLogTail(ctx, e.f.orgID, run.ID, 3)
	if len(tail) != 3 || tail[2] != "== rampa: apply completo ==" {
		t.Errorf("log_tail: %v", tail)
	}

	// Estado cifrado guardado por el tofu falso vía el backend http.
	st, err := e.f.st.GetInfraState(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Ciphertext) == 0 || strings.Contains(string(st.Ciphertext), "aws_eks_cluster") || st.LockInfo != nil {
		t.Errorf("estado: %d bytes, lock %s", len(st.Ciphertext), st.LockInfo)
	}
	plain, err := e.svc.cipher.Open(st.Ciphertext, []byte(run.ID))
	if err != nil || !strings.Contains(string(plain), `"lineage":"fake"`) {
		t.Errorf("descifrado: %v %s", err, plain)
	}

	// Eventos: transiciones de estado y líneas en vivo, más cluster.infra_ready.
	waitFor(t, "eventos del run", 5*time.Second, func() bool {
		seq := e.statusSequence(run.ID)
		return len(seq) > 0 && seq[len(seq)-1] == store.InfraRunSucceeded
	})
	seq := e.statusSequence(run.ID)
	wantSeq := []string{store.InfraRunQueued, store.InfraRunInitializing, store.InfraRunPlanning, store.InfraRunApplying, store.InfraRunSucceeded}
	if strings.Join(seq, ",") != strings.Join(wantSeq, ",") {
		t.Errorf("secuencia de estados: %v", seq)
	}
	kinds := e.eventKinds(run.ID)
	lineEvents := 0
	for _, k := range kinds {
		if k == "infra_run.line" {
			lineEvents++
		}
	}
	if lineEvents < 5 {
		t.Errorf("esperaba eventos infra_run.line, hay %d en %v", lineEvents, kinds)
	}
	found := false
	for _, k := range e.eventKinds(cl.ID) {
		if k == "cluster.infra_ready" {
			found = true
		}
	}
	if !found {
		t.Errorf("falta cluster.infra_ready: %v", e.eventKinds(cl.ID))
	}
	// Las líneas no se persisten como eventos.
	evs, _ := e.f.st.ListEvents(ctx, e.f.orgID, store.EventFilter{SubjectType: SubjectType, SubjectID: run.ID, Limit: 500})
	for _, ev := range evs {
		if ev.Kind == "infra_run.line" {
			t.Fatal("infra_run.line no debe persistirse en events")
		}
	}
	// Los eventos persistidos tampoco llevan credenciales.
	for _, ev := range evs {
		if strings.Contains(string(ev.Payload), fakeSecret) {
			t.Fatal("un evento contiene la credencial")
		}
	}

	// Retry de un run que no falló: conflict.
	if _, err := e.svc.Retry(ctx, e.f.orgID, run.ID, store.Actor{Type: "user"}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("retry de un succeeded debería ser conflict: %v", err)
	}

	// Destroy: mismo run, estado destroying -> destroyed, cluster borrado, estado vaciado.
	d, err := e.svc.Destroy(ctx, e.f.orgID, run.ID, store.Actor{Type: "user"})
	if err != nil || d.Status != store.InfraRunDestroying || d.Operation != store.InfraOpDestroy {
		t.Fatalf("destroy: %v %+v", err, d)
	}
	if _, err := e.svc.Destroy(ctx, e.f.orgID, run.ID, store.Actor{Type: "user"}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("destroy repetido mientras corre debería ser conflict: %v", err)
	}
	done = e.waitStatus(run.ID, store.InfraRunDestroyed)
	if done.Attempt != 2 || done.Error != "" {
		t.Errorf("run destruido: %+v", done)
	}
	log = e.logText(run.ID)
	if !strings.Contains(log, "$ tofu destroy -no-color -input=false -auto-approve") || !strings.Contains(log, "estado previo: HTTP 200") || !strings.Contains(log, "Destroy complete!") || !strings.Contains(log, "(intento 2)") {
		t.Errorf("log del destroy:\n%s", log)
	}
	if strings.Count(log, "$ tofu init") != 2 {
		t.Errorf("el log conserva el apply y agrega el destroy:\n%s", log)
	}
	if _, err := e.f.st.GetCluster(ctx, e.f.orgID, cl.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("el cluster debería haberse borrado tras el destroy: %v", err)
	}
	st, err = e.f.st.GetInfraState(ctx, run.ID)
	if err != nil || len(st.Ciphertext) != 0 {
		t.Errorf("estado tras destroy: %v %d bytes", err, len(st.Ciphertext))
	}
	if n, _ := e.f.st.CountRunningInfraRuns(ctx); n != 0 {
		t.Errorf("no debería quedar ningún run tomado, hay %d", n)
	}
}

func TestRunnerFailureRetryAndAccessDeniedHint(t *testing.T) {
	e := newRunnerEnv(t)
	run, cl := e.createCluster("fail-plan")
	e.startWorker()
	failed := e.waitStatus(run.ID, store.InfraRunFailed)
	if !strings.Contains(failed.Error, "tofu plan falló (código 1)") || !strings.Contains(failed.Error, "UnauthorizedOperation") || !strings.Contains(failed.Error, "ManageInfrastructure=true") {
		t.Errorf("error del run: %q", failed.Error)
	}
	if failed.FinishedAt == nil {
		t.Error("finished_at vacío")
	}
	clAfter, err := e.f.st.GetCluster(context.Background(), e.f.orgID, cl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if clAfter.Status != store.ClusterFailed || clAfter.InfraRunID != run.ID || clAfter.AgentRoleARN != "" {
		t.Errorf("cluster tras el fallo: %+v", clAfter)
	}
	seq := e.statusSequence(run.ID)
	if len(seq) < 4 || seq[len(seq)-1] != store.InfraRunFailed || seq[len(seq)-2] != store.InfraRunPlanning {
		t.Errorf("secuencia: %v", seq)
	}
	log := e.logText(run.ID)
	if !strings.Contains(log, "Error: creating EC2 VPC") || !strings.Contains(log, "== rampa: error:") || strings.Contains(log, "$ tofu apply") {
		t.Errorf("log del fallo:\n%s", log)
	}
	// MarkAllClustersOffline (arranque de la API) no toca un cluster failed.
	if err := e.f.st.MarkAllClustersOffline(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.f.st.GetCluster(context.Background(), e.f.orgID, cl.ID); c.Status != store.ClusterFailed {
		t.Errorf("el arranque no debería pisar failed: %s", c.Status)
	}

	// Retry: vuelve a queued y falla otra vez (el nombre sigue siendo fail-plan), intento 2.
	retried, err := e.svc.Retry(context.Background(), e.f.orgID, run.ID, store.Actor{Type: "user"})
	if err != nil || retried.Status != store.InfraRunQueued || retried.Error != "" || retried.FinishedAt != nil {
		t.Fatalf("retry: %v %+v", err, retried)
	}
	waitFor(t, "segundo intento", 30*time.Second, func() bool {
		r := e.run(run.ID)
		return r.Status == store.InfraRunFailed && r.Attempt == 2
	})
	if !strings.Contains(e.logText(run.ID), "(intento 2)") {
		t.Error("el log debería marcar el segundo intento")
	}
	// Destroy de un run failed se permite (limpia lo que haya quedado) y borra el cluster.
	if _, err := e.svc.Destroy(context.Background(), e.f.orgID, run.ID, store.Actor{Type: "user"}); err != nil {
		t.Fatal(err)
	}
	e.waitStatus(run.ID, store.InfraRunDestroyed)
	if _, err := e.f.st.GetCluster(context.Background(), e.f.orgID, cl.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cluster tras destroy: %v", err)
	}
}

func TestRunnerCredentialsProviderMissing(t *testing.T) {
	e := newRunnerEnv(t)
	// Sin CredentialsFunc de test, el runner busca el provider registrado: en
	// este proceso no hay ninguno para aws, así que falla con un mensaje claro.
	e.svc.SetCredentialsFunc(e.svc.providerCredentials)
	run, _ := e.createCluster("sin-creds")
	e.startWorker()
	failed := e.waitStatus(run.ID, store.InfraRunFailed)
	if !strings.Contains(failed.Error, "obteniendo credenciales") || !strings.Contains(failed.Error, "no está registrado") {
		t.Errorf("error: %q", failed.Error)
	}
	// Si el provider existe pero no implementa CredentialsProvider, también.
	// Queda registrado para el resto del proceso; ningún otro test de este
	// paquete usa el provider aws real.
	provider.Register(noCredsProvider{})
	retried, err := e.svc.Retry(context.Background(), e.f.orgID, run.ID, store.Actor{Type: "user"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "segundo intento", 30*time.Second, func() bool {
		r := e.run(retried.ID)
		return r.Status == store.InfraRunFailed && r.Attempt == 2
	})
	if r := e.run(run.ID); !strings.Contains(r.Error, "no implementa CredentialsProvider") {
		t.Errorf("error: %q", r.Error)
	}
}

// TestRunnerDigitalOceanVM: un módulo vm de DigitalOcean con el provider real
// de digitalocean como CredentialsProvider. El token de la cuenta sale del
// cifrado, llega al subproceso como DIGITALOCEAN_TOKEN y nunca al log; no se
// exportan credenciales de AWS; el cluster queda kind vm con secret_store
// kubernetes y los outputs de la VM (public_ip, ssh_hint, dns_hint) se guardan.
func TestRunnerDigitalOceanVM(t *testing.T) {
	const doToken = "dop_v1_0123456789abcdefFAKE"
	e := newRunnerEnv(t)
	e.svc.SetCredentialsFunc(e.svc.providerCredentials)
	acc := e.f.tokenAccount("acme-do", "digitalocean", testKey(3), map[string]string{"api_token": doToken}, map[string]string{"registry_name": "acme"})
	if !acc.HasCredentials {
		t.Fatalf("la cuenta debería tener credenciales: %+v", acc)
	}
	ctx := context.Background()
	run, cl, err := e.svc.CreateClusterRun(ctx, ClusterRequest{
		OrgID: e.f.orgID, RequestedBy: e.f.user.ID, Account: acc, Module: "digitalocean/droplet-k3s", Name: "do-nonprod", Env: "nonprod",
		Domain: "apps.dev.acme.com", IngressClass: "traefik",
		Inputs: map[string]any{"size": "s-2vcpu-4gb", "admin_cidrs": []any{"203.0.113.0/24"}, "registry_name": "acme"},
		Actor:  store.Actor{Type: "user", ID: e.f.user.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Kind != store.InfraKindVM || run.Module != "digitalocean/droplet-k3s" || run.Provider != "digitalocean" {
		t.Fatalf("run: %+v", run)
	}
	if cl.Kind != store.ClusterKindVM || cl.SecretStore != store.SecretStoreKubernetes || cl.Domain != "apps.dev.acme.com" {
		t.Fatalf("cluster: %+v", cl)
	}
	var inputs map[string]any
	_ = json.Unmarshal(run.Inputs, &inputs)
	if inputs["region"] != "nyc3" || inputs["size"] != "s-2vcpu-4gb" || inputs["image"] != "ubuntu-24-04-x64" || inputs["domain"] != "apps.dev.acme.com" || inputs["registry_name"] != "acme" || inputs["agent_chart_url"] != "http://api.test.local/public/charts/rampa-agent.tgz" {
		t.Errorf("inputs: %v", inputs)
	}
	for _, bad := range []string{"agent_chart_path", "install_agent", "agent_token", "bootstrap_token", "api_token"} {
		if _, has := inputs[bad]; has {
			t.Errorf("inputs no debería tener %s: %v", bad, inputs)
		}
	}
	if strings.Contains(string(run.Inputs), doToken) {
		t.Fatal("el token de la cuenta no puede estar en los inputs")
	}
	// Un módulo de otro provider o inputs inválidos se rechazan antes de crear nada.
	if _, _, err := e.svc.CreateClusterRun(ctx, ClusterRequest{OrgID: e.f.orgID, Account: acc, Module: "aws/ec2-k3s", Name: "x", Env: "nonprod", Domain: "d"}); err == nil {
		t.Error("un módulo de aws con una cuenta de digitalocean debería fallar")
	}
	if _, _, err := e.svc.CreateClusterRun(ctx, ClusterRequest{OrgID: e.f.orgID, Account: acc, Module: "digitalocean/droplet-k3s", Name: "y", Env: "nonprod", Domain: "d", Inputs: map[string]any{"size": "enorme"}}); err == nil {
		t.Error("un size fuera de las opciones debería fallar")
	}

	e.startWorker()
	done := e.waitStatus(run.ID, store.InfraRunSucceeded)
	if done.Error != "" {
		t.Fatalf("run: %+v", done)
	}
	var outputs map[string]json.RawMessage
	if err := json.Unmarshal(done.Outputs, &outputs); err != nil {
		t.Fatal(err)
	}
	var ip, ssh string
	_ = json.Unmarshal(outputs["public_ip"], &ip)
	_ = json.Unmarshal(outputs["ssh_hint"], &ssh)
	if ip != "203.0.113.10" || !strings.Contains(ssh, "ssh root@203.0.113.10") {
		t.Errorf("outputs: %s", done.Outputs)
	}
	if _, has := outputs["dns_hint"]; !has {
		t.Errorf("falta dns_hint: %s", done.Outputs)
	}
	if _, leaked := outputs["secreto"]; leaked {
		t.Error("un output sensible no debe guardarse")
	}
	log := e.logText(run.ID)
	for _, want := range []string{"módulo: digitalocean/droplet-k3s", "bootstrap_token presente: si", "credenciales presentes: DIGITALOCEAN_TOKEN=si AWS_ACCESS_KEY_ID=no TF_VAR_agent_token=no", "agent_chart_url", "rampa-agent.tgz", "token de larga duración", "digitalocean_droplet.this: Creating", "== rampa: apply completo =="} {
		if !strings.Contains(log, want) {
			t.Errorf("falta %q en el log:\n%s", want, log)
		}
	}
	for _, secret := range []string{doToken, "rampa_bootstrap_", "rampa_cluster_", "rampa_state_"} {
		if strings.Contains(log, secret) {
			t.Errorf("el log contiene %q:\n%s", secret, log)
		}
	}
	clAfter, err := e.f.st.GetCluster(ctx, e.f.orgID, cl.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Sin agent_token en el módulo el token del cluster no se rota acá: lo
	// genera el canje del bootstrap. Sigue offline hasta que el agente conecte.
	if clAfter.Status != store.ClusterOffline || clAfter.TokenHash != cl.TokenHash || clAfter.Kind != store.ClusterKindVM {
		t.Errorf("cluster tras el apply: %+v", clAfter)
	}
	bts, err := e.f.st.ListBootstrapTokens(ctx, e.f.orgID, cl.ID)
	if err != nil || len(bts) != 1 || bts[0].UsedAt != nil {
		t.Errorf("bootstrap tokens: %v %+v", err, bts)
	}
	// Los eventos persistidos no llevan el token.
	evs, _ := e.f.st.ListEvents(ctx, e.f.orgID, store.EventFilter{Limit: 500})
	for _, ev := range evs {
		if strings.Contains(string(ev.Payload), doToken) || strings.Contains(ev.Message, doToken) {
			t.Fatal("un evento contiene el token")
		}
	}
	// Sin la clave de cifrado, el run falla con un mensaje claro en vez de correr sin credenciales.
	e.svc.cipher = nil
	if _, err := e.svc.providerAccount(ctx, acc); err == nil || !strings.Contains(err.Error(), "RAMPA_STATE_KEY") {
		t.Errorf("sin cipher debería fallar: %v", err)
	}
}

// noCredsProvider es un provider aws sin CredentialsProvider.
type noCredsProvider struct{}

func (noCredsProvider) Kind() provider.Kind { return provider.AWS }
func (noCredsProvider) DisplayName() string { return "AWS sin credenciales" }
func (noCredsProvider) Available() bool     { return true }
func (noCredsProvider) Auth() provider.AuthKind {
	return provider.AuthFederated
}
func (noCredsProvider) Terminology() provider.Terminology { return provider.Terminology{} }
func (noCredsProvider) ConnectInstructions(provider.ConnectRequest) provider.Instructions {
	return provider.Instructions{}
}
func (noCredsProvider) Verify(context.Context, provider.CloudAccount) (provider.Verification, error) {
	return provider.Verification{OK: true}, nil
}
func (noCredsProvider) Registry(provider.CloudAccount) (string, error) { return "r", nil }
func (noCredsProvider) SecretStoreSpec(provider.CloudAccount, provider.ClusterInfo) provider.SecretStoreSpec {
	return provider.SecretStoreSpec{}
}
func (noCredsProvider) WorkloadIdentityAnnotations(provider.CloudAccount, string) map[string]string {
	return nil
}
func (noCredsProvider) ServiceModule(string) (provider.ModuleRef, bool) {
	return provider.ModuleRef{}, false
}

func TestRecoverOrphanRuns(t *testing.T) {
	e := newRunnerEnv(t)
	run, cl := e.createCluster("huerfano")
	ctx := context.Background()
	// Simula un proceso anterior que tomó el run y murió.
	claimed, err := e.f.st.ClaimInfraRun(ctx, "proceso-viejo")
	if err != nil || claimed.ID != run.ID || claimed.LockedAt == nil {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	e.svc.Recover(ctx)
	r := e.run(run.ID)
	if r.Status != store.InfraRunFailed || r.Error != OrphanMessage || r.FinishedAt == nil || r.LockedAt != nil {
		t.Errorf("run recuperado: %+v", r)
	}
	if c, _ := e.f.st.GetCluster(ctx, e.f.orgID, cl.ID); c.Status != store.ClusterFailed {
		t.Errorf("cluster: %s", c.Status)
	}
	// Idempotente: sin runs tomados no hace nada, y el fallo quedó anunciado.
	e.svc.Recover(ctx)
	waitFor(t, "evento failed", 5*time.Second, func() bool {
		seq := e.statusSequence(run.ID)
		return len(seq) == 2 && seq[1] == store.InfraRunFailed
	})
}

func TestClaimOnePerCloudAccount(t *testing.T) {
	e := newRunnerEnv(t)
	ctx := context.Background()
	r1, _ := e.createCluster("uno")
	r2, _ := e.createCluster("dos")
	otherAcc := e.f.connectedAccount("acme-aws-2")
	r3, _, err := e.svc.CreateClusterRun(ctx, ClusterRequest{OrgID: e.f.orgID, Account: otherAcc, Name: "tres", Env: "prod", Domain: "apps.acme.com", IngressClass: "nginx"})
	if err != nil {
		t.Fatal(err)
	}
	c1, err := e.f.st.ClaimInfraRun(ctx, "w")
	if err != nil || c1.ID != r1.ID {
		t.Fatalf("primer claim: %v %+v", err, c1)
	}
	// r2 es de la misma cuenta que r1 (en curso): no se toma; r3 es de otra cuenta y sí.
	c2, err := e.f.st.ClaimInfraRun(ctx, "w")
	if err != nil || c2.ID != r3.ID {
		t.Fatalf("segundo claim debería ser el de la otra cuenta: %v %+v", err, c2)
	}
	if _, err := e.f.st.ClaimInfraRun(ctx, "w"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("tercer claim debería no encontrar nada: %v", err)
	}
	if _, err := e.f.st.FinishInfraRun(ctx, e.f.orgID, r1.ID, store.InfraRunSucceeded, "", nil); err != nil {
		t.Fatal(err)
	}
	c3, err := e.f.st.ClaimInfraRun(ctx, "w")
	if err != nil || c3.ID != r2.ID || c3.Attempt != 1 {
		t.Fatalf("tras terminar r1 se toma r2: %v %+v", err, c3)
	}
	if n, _ := e.f.st.CountRunningInfraRuns(ctx); n != 2 {
		t.Errorf("runs en curso: %d", n)
	}
	runs, err := e.f.st.ListInfraRuns(ctx, e.f.orgID, 10)
	if err != nil || len(runs) != 3 || runs[0].ID != r3.ID {
		t.Errorf("listado (más nuevo primero): %v %d", err, len(runs))
	}
	// Otra org no ve estos runs.
	if _, err := e.f.st.GetInfraRun(ctx, "00000000-0000-0000-0000-000000000000", r1.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("otra org: %v", err)
	}
}
