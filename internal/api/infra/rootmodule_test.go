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
	if len(mods) != 5 || !mods[0].Available || mods[0].Module != "aws/eks-cluster" {
		t.Fatalf("módulos: %+v", mods)
	}
	for _, m := range mods[1:] {
		if m.Available || m.Inputs == nil {
			t.Errorf("%s debería estar no disponible y con inputs []: %+v", m.Module, m)
		}
	}
	eks, ok := FindModule("aws", "cluster")
	if !ok || len(eks.Inputs) != 3 || eks.Inputs[0].Name != "kubernetes_version" || eks.Inputs[0].Default != "1.33" || len(eks.Inputs[0].Options) != 4 {
		t.Errorf("aws/eks-cluster: %+v", eks)
	}
	if _, ok := FindModule("gcp", "cluster"); !ok {
		t.Error("gcp/gke-cluster debería figurar aunque no esté disponible")
	}
	if _, ok := FindModuleByPath("aws/eks-cluster"); !ok {
		t.Error("FindModuleByPath")
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
		"variable \"admin_principal_arns\" {\n  type = list(string)\n  default = []\n}",
		"  agent_chart_path = var.agent_chart_path\n",
		"output \"agent_role_arn\" {\n  value = module.cluster.agent_role_arn\n}",
		`source  = "hashicorp/helm"`,
	} {
		if !strings.Contains(tf, want) {
			t.Errorf("falta %q en el root module:\n%s", want, tf)
		}
	}
	if strings.Contains(tf, "  region = var.region\n}\n\nmodule") || strings.Contains(tf, "\n  region = var.region\n  ") {
		// region configura el provider, no es input del módulo.
		if strings.Count(tf, "region = var.region") != 1 {
			t.Errorf("region debería aparecer solo en el provider:\n%s", tf)
		}
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
// módulo embebido y chart) y lo valida con tofu init y tofu validate, que es
// lo que va a correr el runner. Necesita tofu en el PATH y red para bajar los
// providers la primera vez; RAMPA_TEST_SKIP_TOFU_INIT=1 lo salta.
func TestRootModuleValidatesWithTofu(t *testing.T) {
	tofu, err := exec.LookPath("tofu")
	if err != nil {
		t.Skip("sin tofu en el PATH")
	}
	if os.Getenv("RAMPA_TEST_SKIP_TOFU_INIT") == "1" {
		t.Skip("RAMPA_TEST_SKIP_TOFU_INIT=1")
	}
	m, ok := FindModule("aws", "cluster")
	if !ok {
		t.Fatal("no encuentro el módulo aws/cluster")
	}
	wd, err := WriteWorkdir(t.TempDir(), m)
	if err != nil {
		t.Fatal(err)
	}
	if err := wd.WriteTFVars(map[string]any{
		"region": "us-east-1", "name": "rampa-test", "env": "nonprod",
		"kubernetes_version": "1.33", "admin_principal_arns": []string{},
		"install_agent": true, "api_url": "http://localhost:8080", "agent_chart_path": wd.AgentChartPath,
	}); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(os.TempDir(), "rampa-tofu-plugin-cache")
	_ = os.MkdirAll(cache, 0o755)
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
}
