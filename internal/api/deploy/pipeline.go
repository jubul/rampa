// Package deploy es la pipeline de render del plano de control: rampa.yaml
// más scope más cluster se convierten en values de rampa-app, el chart
// embebido se renderiza con el SDK de Helm y los objetos resultantes viajan
// al agente en un protocol.DeployCommand.
package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	yamlv3 "gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	sigyaml "sigs.k8s.io/yaml"

	"rampa.dev/charts"
	"rampa.dev/pkg/manifest"
	"rampa.dev/pkg/protocol"
	"rampa.dev/pkg/render"
)

// Labels que la API pone en todo lo que manda al agente.
const (
	LabelApp        = "rampa.dev/app"
	LabelScope      = "rampa.dev/scope"
	LabelDeployment = "rampa.dev/deployment"
	LabelManagedBy  = "app.kubernetes.io/managed-by"
)

const chartPrefix = "rampa-app/"

var docSeparator = regexp.MustCompile(`(?m)^---[ \t]*$`)

// Pipeline renderiza deployments. Es segura para uso concurrente: el chart
// cargado es de solo lectura.
type Pipeline struct {
	chart   *chart.Chart
	timeout time.Duration
}

// New carga el chart embebido. timeout es RAMPA_DEPLOY_TIMEOUT.
func New(timeout time.Duration) (*Pipeline, error) {
	ch, err := LoadChart()
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &Pipeline{chart: ch, timeout: timeout}, nil
}

// Chart devuelve el chart cargado (para inspección y tests).
func (p *Pipeline) Chart() *chart.Chart { return p.chart }

// LoadChart recorre charts.RampaApp y lo carga con el loader de Helm.
func LoadChart() (*chart.Chart, error) {
	var files []*loader.BufferedFile
	err := fs.WalkDir(charts.RampaApp, "rampa-app", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(charts.RampaApp, path)
		if err != nil {
			return fmt.Errorf("leyendo %s del chart embebido: %w", path, err)
		}
		files = append(files, &loader.BufferedFile{Name: strings.TrimPrefix(path, chartPrefix), Data: data})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("recorriendo el chart embebido: %w", err)
	}
	if len(files) == 0 {
		return nil, errors.New("el chart embebido está vacío")
	}
	ch, err := loader.LoadFiles(files)
	if err != nil {
		return nil, fmt.Errorf("cargando el chart rampa-app: %w", err)
	}
	return ch, nil
}

// Target es el cluster y registry donde se despliega.
type Target struct {
	Registry     string
	Domain       string
	IngressClass string
	SecretStore  string
}

// Values arma los values del chart para un scope con pkg/render.
func (p *Pipeline) Values(app *manifest.Application, scope, image string, t Target) (*render.Values, []string, error) {
	opts := render.Options{
		Image:    image,
		Registry: t.Registry,
		Cluster: &render.Cluster{
			Domain:       t.Domain,
			IngressClass: t.IngressClass,
			SecretStore:  t.SecretStore,
		},
	}
	v, warns, err := render.Build(app, scope, opts)
	if err != nil {
		return nil, nil, err
	}
	return v, warns, nil
}

// ValuesToMap convierte los values tipados al mapa que consume Helm.
func ValuesToMap(v *render.Values) (map[string]any, error) {
	b, err := yamlv3.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("serializando values: %w", err)
	}
	vals, err := chartutil.ReadValues(b)
	if err != nil {
		return nil, fmt.Errorf("releyendo values: %w", err)
	}
	return vals.AsMap(), nil
}

// Rendered es la salida del render del chart.
type Rendered struct {
	Objects []json.RawMessage
	Kinds   []string // kind de cada objeto, en el mismo orden
}

// RenderObjects renderiza el chart con los values y devuelve los objetos de
// Kubernetes en JSON, sin documentos vacíos ni NOTES.txt.
func (p *Pipeline) RenderObjects(v *render.Values) (*Rendered, error) {
	vals, err := ValuesToMap(v)
	if err != nil {
		return nil, err
	}
	return p.RenderObjectsFromMap(vals, v.App.Name, v.App.Name+"-"+v.Scope.Name)
}

// RenderObjectsFromMap es RenderObjects a partir de values ya en mapa (por
// ejemplo, los guardados en la fila del deployment).
func (p *Pipeline) RenderObjectsFromMap(vals map[string]any, releaseName, namespace string) (*Rendered, error) {
	opts := chartutil.ReleaseOptions{Name: releaseName, Namespace: namespace, Revision: 1, IsInstall: true}
	renderVals, err := chartutil.ToRenderValues(p.chart, vals, opts, chartutil.DefaultCapabilities)
	if err != nil {
		return nil, fmt.Errorf("preparando values para el chart: %w", err)
	}
	files, err := engine.Render(p.chart, renderVals)
	if err != nil {
		return nil, fmt.Errorf("renderizando el chart: %w", err)
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	out := &Rendered{}
	for _, name := range names {
		if strings.HasSuffix(name, "NOTES.txt") {
			continue
		}
		for _, doc := range docSeparator.Split(files[name], -1) {
			if isEmptyDoc(doc) {
				continue
			}
			j, err := sigyaml.YAMLToJSON([]byte(doc))
			if err != nil {
				return nil, fmt.Errorf("%s: YAML inválido: %w", name, err)
			}
			if string(j) == "null" || string(j) == "{}" {
				continue
			}
			var probe struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal(j, &probe); err != nil || probe.Kind == "" {
				return nil, fmt.Errorf("%s: el documento no es un objeto de Kubernetes", name)
			}
			out.Objects = append(out.Objects, json.RawMessage(j))
			out.Kinds = append(out.Kinds, probe.Kind)
		}
	}
	if len(out.Objects) == 0 {
		return nil, errors.New("el chart no produjo ningún objeto")
	}
	return out, nil
}

// isEmptyDoc dice si un documento YAML solo tiene espacios y comentarios.
func isEmptyDoc(doc string) bool {
	for _, line := range strings.Split(doc, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		return false
	}
	return true
}

// CommandInput es lo que hace falta para armar un DeployCommand.
type CommandInput struct {
	DeploymentID string
	App          string
	Scope        string
	Objects      []json.RawMessage
}

// BuildCommand arma el protocol.Command de deploy. El id del comando es nuevo
// y también es el id de la fila en agent_commands.
func (p *Pipeline) BuildCommand(in CommandInput) protocol.Command {
	return protocol.Command{
		ID:   uuid.NewString(),
		Kind: protocol.CommandDeploy,
		Deploy: &protocol.DeployCommand{
			DeploymentID:   in.DeploymentID,
			App:            in.App,
			Scope:          in.Scope,
			Namespace:      in.App + "-" + in.Scope,
			Labels:         Labels(in.App, in.Scope, in.DeploymentID),
			Objects:        in.Objects,
			TimeoutSeconds: int(p.timeout.Seconds()),
		},
	}
}

// UndeployCommand arma el comando para borrar el namespace de un scope.
func UndeployCommand(app, scope string) protocol.Command {
	return protocol.Command{
		ID:   uuid.NewString(),
		Kind: protocol.CommandUndeploy,
		Undeploy: &protocol.UndeployCommand{
			App:       app,
			Scope:     scope,
			Namespace: app + "-" + scope,
			Wait:      true,
		},
	}
}

// Labels son las etiquetas que identifican lo que aplica un deployment.
func Labels(app, scope, deploymentID string) map[string]string {
	return map[string]string{
		LabelApp:        app,
		LabelScope:      scope,
		LabelDeployment: deploymentID,
		LabelManagedBy:  "rampa",
	}
}

// Timeout devuelve el timeout configurado.
func (p *Pipeline) Timeout() time.Duration { return p.timeout }
