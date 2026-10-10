package infra

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestModulesRegistry(t *testing.T) {
	mods := Modules()
	if len(mods) != 9 || !mods[0].Available || mods[0].Module != "aws/eks-cluster" {
		t.Fatalf("módulos: %+v", mods)
	}
	wantOrder := []string{"aws/eks-cluster", "aws/ec2-k3s", "digitalocean/doks-cluster", "digitalocean/droplet-k3s"}
	for i, want := range wantOrder {
		if mods[i].Module != want || !mods[i].Available {
			t.Errorf("módulo %d = %s (available %v), quería %s disponible", i, mods[i].Module, mods[i].Available, want)
		}
	}
	for _, m := range mods[4:] {
		if m.Available || m.Inputs == nil {
			t.Errorf("%s debería estar no disponible y con inputs []: %+v", m.Module, m)
		}
	}
	eks, ok := FindModule("aws", "cluster")
	if !ok || len(eks.Inputs) != 3 || eks.Inputs[0].Name != "kubernetes_version" || eks.Inputs[0].Default != "1.33" || len(eks.Inputs[0].Options) != 4 || eks.SecretStore != "aws-secrets-manager" {
		t.Errorf("aws/eks-cluster: %+v", eks)
	}
	ec2, ok := FindModule("aws", "vm")
	if !ok || ec2.Module != "aws/ec2-k3s" || ec2.ClusterKind() != "vm" || ec2.SecretStore != "aws-secrets-manager" {
		t.Errorf("aws/ec2-k3s: %+v", ec2)
	}
	droplet, ok := FindModuleByPath("digitalocean/droplet-k3s")
	if !ok || droplet.Kind != KindVM || droplet.SecretStore != "kubernetes" || !droplet.HasVariable("domain") || droplet.HasVariable("agent_chart_path") {
		t.Errorf("digitalocean/droplet-k3s: %+v", droplet)
	}
	doks, ok := FindModule("digitalocean", "cluster")
	if !ok || doks.Module != "digitalocean/doks-cluster" || doks.ClusterKind() != "kubernetes" || doks.SecretStore != "kubernetes" {
		t.Errorf("digitalocean/doks-cluster: %+v", doks)
	}
	// Los inputs exactos que documenta docs/api.md.
	wantInputs := map[string][]string{
		"aws/eks-cluster":           {"kubernetes_version", "vpc_cidr", "admin_principal_arns"},
		"aws/ec2-k3s":               {"instance_type", "k3s_version", "key_name", "admin_cidrs", "route53_zone_id", "tls_email", "volume_size_gb"},
		"digitalocean/droplet-k3s":  {"size", "image", "k3s_version", "ssh_key_fingerprints", "admin_cidrs", "dns_zone", "tls_email", "registry_name"},
		"digitalocean/doks-cluster": {"kubernetes_version", "node_size", "node_count", "registry_name"},
	}
	for path, names := range wantInputs {
		m, _ := FindModuleByPath(path)
		got := make([]string, 0, len(m.Inputs))
		for _, in := range m.Inputs {
			got = append(got, in.Name)
		}
		if strings.Join(got, ",") != strings.Join(names, ",") {
			t.Errorf("%s inputs = %v, quería %v", path, got, names)
		}
		// Todo módulo disponible declara las variables comunes y el runner
		// conoce las mismas variables que el módulo embebido.
		for _, common := range []string{"name", "env", "api_url", "bootstrap_token", "agent_chart_url", "agent_image_repository", "agent_image_tag", "tags"} {
			if !m.HasVariable(common) {
				t.Errorf("%s: el root no declara la variable común %s", path, common)
			}
		}
		for _, v := range ModuleVariableNames(m) {
			if !m.HasVariable(v) && !InternalModuleVariables[v] {
				t.Errorf("%s: el módulo embebido declara %s y el root no la conoce", path, v)
			}
		}
	}
	if _, ok := FindModule("gcp", "cluster"); !ok {
		t.Error("gcp/gke-cluster debería figurar aunque no esté disponible")
	}
	if _, ok := FindModuleByPath("aws/eks-cluster"); !ok {
		t.Error("FindModuleByPath")
	}
	if _, err := ResolveModule("digitalocean", ""); err != nil {
		t.Errorf("el default de digitalocean es doks: %v", err)
	}
	if _, err := ResolveModule("digitalocean", "aws/ec2-k3s"); err == nil {
		t.Error("un módulo de otro provider debería rechazarse")
	}
	if _, err := ResolveModule("gcp", ""); err == nil {
		t.Error("gcp no tiene módulo disponible")
	}
}

func TestValidateInputs(t *testing.T) {
	droplet, _ := FindModuleByPath("digitalocean/droplet-k3s")
	got, err := ValidateInputs(droplet, map[string]any{"admin_cidrs": []any{"203.0.113.0/24", " 198.51.100.0/24 "}, "registry_name": "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if got["size"] != "s-2vcpu-2gb" || got["image"] != "ubuntu-24-04-x64" || got["k3s_version"] != "" || got["registry_name"] != "acme" {
		t.Errorf("defaults: %v", got)
	}
	if cidrs, _ := got["admin_cidrs"].([]string); len(cidrs) != 2 || cidrs[1] != "198.51.100.0/24" {
		t.Errorf("admin_cidrs: %v", got["admin_cidrs"])
	}
	if fps, _ := got["ssh_key_fingerprints"].([]string); fps == nil || len(fps) != 0 {
		t.Errorf("las listas ausentes quedan []: %#v", got["ssh_key_fingerprints"])
	}
	bad := map[string]map[string]any{
		"opción":       {"size": "s-96vcpu-384gb"},
		"tipo lista":   {"admin_cidrs": "no-es-cidr"},
		"cidr":         {"admin_cidrs": []any{"300.0.0.0/8"}},
		"desconocido":  {"vpc_cidr": "10.0.0.0/16"},
		"fingerprint":  {"ssh_key_fingerprints": []any{"hola"}},
		"tipo string":  {"size": 3},
		"email":        {"tls_email": "no-es-email"},
		"dns":          {"dns_zone": "-mal-"},
		"k3s":          {"k3s_version": "1.31"},
		"registry":     {"registry_name": "Acme"},
		"lista de int": {"admin_cidrs": []any{1, 2}},
	}
	for name, raw := range bad {
		if _, err := ValidateInputs(droplet, raw); err == nil {
			t.Errorf("%s: esperaba error con %v", name, raw)
		} else if _, ok := err.(*InputError); !ok {
			t.Errorf("%s: esperaba InputError, tengo %T", name, err)
		}
	}
	// Números: JSON los trae como float64; también se aceptan como texto.
	doks, _ := FindModuleByPath("digitalocean/doks-cluster")
	got, err = ValidateInputs(doks, map[string]any{"node_count": float64(3), "kubernetes_version": "1.33.1-do.0"})
	if err != nil || got["node_count"] != 3 || got["node_size"] != "s-2vcpu-4gb" {
		t.Errorf("doks: %v %v", got, err)
	}
	if got, err := ValidateInputs(doks, map[string]any{"node_count": "5"}); err != nil || got["node_count"] != 5 {
		t.Errorf("node_count como texto: %v %v", got, err)
	}
	for _, raw := range []map[string]any{{"node_count": 2.5}, {"node_count": 0}, {"node_count": 99}, {"node_count": "x"}, {"kubernetes_version": "1.33"}} {
		if _, err := ValidateInputs(doks, raw); err == nil {
			t.Errorf("esperaba error con %v", raw)
		}
	}
	// El default del formulario también se valida con el módulo de EKS.
	eks, _ := FindModuleByPath("aws/eks-cluster")
	got, err = ValidateInputs(eks, nil)
	if err != nil || got["kubernetes_version"] != "1.33" || got["vpc_cidr"] != DefaultVPCCIDR {
		t.Errorf("eks defaults: %v %v", got, err)
	}
	if _, err := ValidateInputs(eks, map[string]any{"vpc_cidr": "10.0.0.0/8"}); err == nil {
		t.Error("vpc_cidr /8 debería fallar")
	}
	if _, err := ValidateInputs(eks, map[string]any{"admin_principal_arns": []any{"juan"}}); err == nil {
		t.Error("un ARN inválido debería fallar")
	}
	if _, err := ValidateInputs(eks, map[string]any{"kubernetes_version": "1.20"}); err == nil {
		t.Error("una versión fuera de las opciones debería fallar")
	}
	// Un string vacío toma el default y una lista como texto se parte por comas.
	ec2, _ := FindModuleByPath("aws/ec2-k3s")
	got, err = ValidateInputs(ec2, map[string]any{"instance_type": "", "admin_cidrs": "203.0.113.0/24, 198.51.100.0/24", "volume_size_gb": 50})
	if err != nil || got["instance_type"] != "t3.small" || got["volume_size_gb"] != 50 || len(got["admin_cidrs"].([]string)) != 2 {
		t.Errorf("ec2: %v %v", got, err)
	}
	if _, err := ValidateInputs(ec2, map[string]any{"volume_size_gb": 5}); err == nil {
		t.Error("un disco de 5 GB debería fallar")
	}
}

func TestRootModule(t *testing.T) {
	m, _ := FindModule("aws", "cluster")
	tf := RootModule(m)
	for _, want := range []string{
		`backend "http" {}`,
		"provider \"aws\" {\n  region = var.region\n}",
		`source = "./modules/aws/eks-cluster"`,
		"variable \"agent_token\" {\n  type = string\n  default = \"\"\n  sensitive = true\n}",
		"variable \"bootstrap_token\" {\n  type = string\n  default = \"\"\n  sensitive = true\n}",
		"variable \"admin_principal_arns\" {\n  type = list(string)\n  default = []\n}",
		"variable \"tags\" {\n  type = map(string)\n  default = {}\n}",
		"  agent_chart_path = var.agent_chart_path != \"\" ? var.agent_chart_path : null\n",
		"  agent_token = var.agent_token != \"\" ? var.agent_token : null\n",
		"output \"agent_role_arn\" {\n  value = module.cluster.agent_role_arn\n}",
		`source  = "hashicorp/helm"`,
	} {
		if !strings.Contains(tf, want) {
			t.Errorf("falta %q en el root module:\n%s", want, tf)
		}
	}
	// region configura el provider, no es input del módulo de EKS.
	if strings.Count(tf, "region = var.region") != 1 {
		t.Errorf("region debería aparecer solo en el provider:\n%s", tf)
	}
	// Al módulo solo se le pasan las variables que declara.
	if strings.Contains(tf, "  bootstrap_token = var.bootstrap_token != \"\" ? var.bootstrap_token : null\n") != ModuleDeclaresVariable(m, "bootstrap_token") {
		t.Errorf("bootstrap_token se pasa al módulo solo si lo declara:\n%s", tf)
	}

	// DigitalOcean: provider sin argumentos (token por DIGITALOCEAN_TOKEN),
	// región como variable del módulo, tags como lista, sin agent_chart_path.
	do, _ := FindModuleByPath("digitalocean/droplet-k3s")
	tf = RootModule(do)
	for _, want := range []string{
		"provider \"digitalocean\" {}\n",
		`source  = "digitalocean/digitalocean"`,
		`version = ">= 2.40"`,
		`source = "./modules/digitalocean/droplet-k3s"`,
		"module \"vm\" {",
		"  region = var.region\n",
		"  bootstrap_token = var.bootstrap_token != \"\" ? var.bootstrap_token : null\n",
		"  agent_chart_url = var.agent_chart_url != \"\" ? var.agent_chart_url : null\n",
		"  domain = var.domain != \"\" ? var.domain : null\n",
		"variable \"tags\" {\n  type = list(string)\n  default = []\n}",
		"output \"public_ip\" {\n  value = module.vm.public_ip\n}",
	} {
		if !strings.Contains(tf, want) {
			t.Errorf("falta %q en el root module de DO:\n%s", want, tf)
		}
	}
	for _, bad := range []string{"provider \"aws\"", "agent_chart_path", "agent_token", "DIGITALOCEAN_TOKEN"} {
		if strings.Contains(tf, bad) {
			t.Errorf("el root module de DO no debería tener %q:\n%s", bad, tf)
		}
	}
	// aws/ec2-k3s: la región va al provider y el módulo no la declara.
	ec2, _ := FindModuleByPath("aws/ec2-k3s")
	tf = RootModule(ec2)
	if strings.Count(tf, "region = var.region") != 1 || !strings.Contains(tf, "  volume_size_gb = var.volume_size_gb\n") || !strings.Contains(tf, "  domain = var.domain != \"\" ? var.domain : null\n") {
		t.Errorf("root module de ec2-k3s:\n%s", tf)
	}
	// Si hay tofu en el PATH, el archivo tiene que ser HCL válido (fmt falla con sintaxis rota).
	tofu, err := exec.LookPath("tofu")
	if err != nil {
		t.Skip("sin tofu en el PATH: no valido la sintaxis del root module")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tf), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(tofu, "fmt", filepath.Join(dir, "main.tf")).CombinedOutput()
	if err != nil {
		t.Fatalf("tofu fmt rechazó el root module: %v\n%s\n%s", err, out, tf)
	}
}

func TestWriteWorkdir(t *testing.T) {
	m, _ := FindModule("aws", "cluster")
	wd, err := WriteWorkdir(t.TempDir(), m)
	if err != nil {
		t.Fatal(err)
	}
	defer wd.Remove()
	for _, f := range []string{"main.tf", "modules/aws/eks-cluster/main.tf", "modules/aws/eks-cluster/variables.tf", "modules/aws/eks-cluster/outputs.tf", "charts/rampa-agent/Chart.yaml", "charts/rampa-agent/values.yaml", "charts/rampa-agent/templates/deployment.yaml"} {
		if _, err := os.Stat(filepath.Join(wd.Dir, f)); err != nil {
			t.Errorf("falta %s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(wd.Dir, "modules/aws/postgres")); err == nil {
		t.Error("solo se escribe el módulo del run, no todos")
	}
	if wd.AgentChartPath != filepath.Join(wd.Dir, "charts", "rampa-agent") {
		t.Errorf("AgentChartPath: %s", wd.AgentChartPath)
	}
	if err := wd.WriteTFVars(map[string]any{"name": "x", "admin_principal_arns": []string{}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(wd.Dir, TFVarsFile))
	if !strings.Contains(string(b), `"name": "x"`) {
		t.Errorf("tfvars: %s", b)
	}
	wd.Remove()
	if _, err := os.Stat(wd.Dir); !os.IsNotExist(err) {
		t.Error("Remove debería borrar el directorio")
	}
}

func TestNewNotReady(t *testing.T) {
	s := New(Config{TofuBin: "/no/existe/tofu"}, nil, nil, nil)
	err := s.Ready()
	if err == nil || !strings.Contains(err.Error(), "RAMPA_TOFU_BIN") || !strings.Contains(err.Error(), "RAMPA_STATE_KEY") {
		t.Errorf("Ready debería explicar qué falta: %v", err)
	}
	s = New(Config{TofuBin: "sh", StateKey: testKey(1)}, nil, nil, nil)
	if err := s.Ready(); err != nil {
		t.Errorf("con binario y clave debería estar listo: %v", err)
	}
	if repo, tag := splitImage("ghcr.io/acme/rampa-agent:1.2"); repo != "ghcr.io/acme/rampa-agent" || tag != "1.2" {
		t.Errorf("splitImage: %s %s", repo, tag)
	}
}

// TestRootModuleValidatesWithTofu escribe el workdir completo (root module,
// módulo embebido y chart) de cada módulo disponible y lo valida con tofu init
// y tofu validate, que es lo que va a correr el runner. Necesita tofu en el
// PATH y red para bajar los providers la primera vez; RAMPA_TEST_SKIP_TOFU_INIT=1 lo salta.
// TestRootModuleOptionalStringsAsNull: un input de texto vacío no debe pisar el
// default del módulo (k3s_version fijada en el módulo rechaza "").
func TestRootModuleOptionalStringsAsNull(t *testing.T) {
	for _, path := range []string{"digitalocean/droplet-k3s", "aws/ec2-k3s"} {
		m, ok := FindModuleByPath(path)
		if !ok {
			t.Fatalf("no encuentro %s", path)
		}
		tf := RootModule(m)
		if !strings.Contains(tf, "  k3s_version = var.k3s_version != \"\" ? var.k3s_version : null\n") {
			t.Errorf("%s: k3s_version vacío debería pasarse como null:\n%s", path, tf)
		}
		if strings.Contains(tf, "  name = var.name != ") {
			t.Errorf("%s: name es requerido y no debería tener el condicional", path)
		}
	}
}

func TestRootModuleValidatesWithTofu(t *testing.T) {
	tofu, err := exec.LookPath("tofu")
	if err != nil {
		t.Skip("sin tofu en el PATH")
	}
	if os.Getenv("RAMPA_TEST_SKIP_TOFU_INIT") == "1" {
		t.Skip("RAMPA_TEST_SKIP_TOFU_INIT=1")
	}
	cache := filepath.Join(os.TempDir(), "rampa-tofu-plugin-cache")
	_ = os.MkdirAll(cache, 0o755)
	for _, m := range Modules() {
		if !m.Available {
			continue
		}
		t.Run(m.Module, func(t *testing.T) {
			wd, err := WriteWorkdir(t.TempDir(), m)
			if err != nil {
				t.Fatal(err)
			}
			vars := map[string]any{
				"region": "us-east-1", "name": "rampa-test", "env": "nonprod", "domain": "apps.dev.acme.com",
				"api_url": "http://localhost:8080", "agent_chart_url": "http://localhost:8080" + AgentChartPath,
			}
			if m.HasVariable("agent_chart_path") {
				vars["agent_chart_path"] = wd.AgentChartPath
			}
			for k := range vars {
				if !m.HasVariable(k) {
					delete(vars, k)
				}
			}
			if err := wd.WriteTFVars(vars); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"init", "-backend=false", "-input=false", "-no-color"},
				{"validate", "-no-color"},
			} {
				cmd := exec.Command(tofu, args...)
				cmd.Dir = wd.Dir
				cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1", "TF_PLUGIN_CACHE_DIR="+cache)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("tofu %s falló: %v\n%s", args[0], err, out)
				}
			}
		})
	}
}
