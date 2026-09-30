package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// ApplicationSpec es la forma estructurada de un rampa.yaml: lo que produce el
// formulario de la web (docs/api.md, "Applications sin YAML"). Es equivalente
// al manifest y se convierte en ambos sentidos sin pérdida: FromSpec arma la
// Application canónica y ToSpec la vuelve a este formato.
type ApplicationSpec struct {
	Name        string          `json:"name"`
	Team        string          `json:"team"`
	Tier        int             `json:"tier"`
	Description string          `json:"description"`
	Type        string          `json:"type"`
	Runtime     string          `json:"runtime"`
	Port        int             `json:"port"`
	Healthcheck string          `json:"healthcheck"`
	Schedule    string          `json:"schedule"`
	Scopes      []SpecScope     `json:"scopes"`
	Parameters  []SpecParameter `json:"parameters"`
	Services    []SpecService   `json:"services"`
}

// SpecScope es un scope del spec. El nombre pasa a ser la clave de spec.scopes
// en el manifest. "pr" es la plantilla de los scopes efímeros.
type SpecScope struct {
	Name        string       `json:"name"`
	Compute     string       `json:"compute"`
	Size        string       `json:"size"`
	Replicas    int          `json:"replicas"`
	Strategy    string       `json:"strategy"`
	Domain      string       `json:"domain"`
	Autoscaling *Autoscaling `json:"autoscaling"`
	TTL         string       `json:"ttl"`
}

// Orígenes posibles de un SpecParameter.
const (
	SourceValue   = "value"
	SourceSecret  = "secret"
	SourceService = "service"
)

// SpecParameter es una variable de entorno con su origen explícito: value
// (usa Value), secret (ningún campo extra) o service (usa Service y Key).
// Scopes vacío significa todos.
type SpecParameter struct {
	Name    string   `json:"name"`
	Source  string   `json:"source"`
	Value   string   `json:"value,omitempty"`
	Service string   `json:"service,omitempty"`
	Key     string   `json:"key,omitempty"`
	Scopes  []string `json:"scopes"`
}

// SpecService es un service gestionado. Scopes vacío significa todos los permanentes.
type SpecService struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Size   string   `json:"size"`
	Scopes []string `json:"scopes"`
}

// ManifestHeader es el comentario con el que empieza el rampa.yaml generado.
const ManifestHeader = "# Generado por Rampa desde la web. Editalo a mano si querés: sigue siendo la fuente de verdad.\n"

// scopeOrder es el orden estable en que se listan los scopes: los permanentes
// del más chico al más grande y la plantilla de efímeros al final.
var scopeOrder = map[string]int{"dev": 0, "staging": 1, "prod": 2, PRScopeKey: 3}

// FromSpec convierte el spec en una Application con los defaults aplicados,
// lista para Validate. Devuelve error (uno o varios, unidos con errors.Join)
// cuando el spec no se puede traducir: un source desconocido, un scope
// repetido o campos que no corresponden al source elegido. Los problemas de
// contenido (nombres, tamaños, rangos) los reporta Validate igual que con un
// manifest pegado.
func FromSpec(spec ApplicationSpec) (*Application, error) {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	app := &Application{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata: Metadata{
			Name:        spec.Name,
			Team:        spec.Team,
			Tier:        spec.Tier,
			Description: spec.Description,
		},
		Spec: Spec{
			Type:        spec.Type,
			Runtime:     spec.Runtime,
			Port:        spec.Port,
			Healthcheck: spec.Healthcheck,
			Schedule:    spec.Schedule,
			Scopes:      map[string]Scope{},
			Parameters:  []Parameter{},
			Services:    []Service{},
		},
	}

	for i, sc := range spec.Scopes {
		if sc.Name == "" {
			add("scopes[%d].name es requerido", i)
			continue
		}
		if _, dup := app.Spec.Scopes[sc.Name]; dup {
			add("scopes[%d]: el scope %q está repetido", i, sc.Name)
			continue
		}
		var as *Autoscaling
		if sc.Autoscaling != nil {
			cp := *sc.Autoscaling
			as = &cp
		}
		app.Spec.Scopes[sc.Name] = Scope{
			Compute:     sc.Compute,
			Size:        sc.Size,
			Replicas:    sc.Replicas,
			Strategy:    sc.Strategy,
			Domain:      sc.Domain,
			Autoscaling: as,
			TTL:         sc.TTL,
		}
	}

	for i, p := range spec.Parameters {
		prm := Parameter{Name: p.Name, Scopes: copyStrings(p.Scopes)}
		switch p.Source {
		case SourceValue:
			prm.Value = p.Value
			if p.Service != "" || p.Key != "" {
				add("parameters[%d] (%s): service y key solo aplican con source service", i, p.Name)
			}
		case SourceSecret:
			prm.Secret = true
			if p.Value != "" || p.Service != "" || p.Key != "" {
				add("parameters[%d] (%s): un secret no lleva value, service ni key", i, p.Name)
			}
		case SourceService:
			prm.FromService = p.Service
			prm.Key = p.Key
			if p.Value != "" {
				add("parameters[%d] (%s): value no aplica con source service", i, p.Name)
			}
		case "":
			add("parameters[%d] (%s): source es requerido: value, secret o service", i, p.Name)
		default:
			add("parameters[%d] (%s): source %q inválido. Opciones: value, secret, service", i, p.Name, p.Source)
		}
		app.Spec.Parameters = append(app.Spec.Parameters, prm)
	}

	for _, s := range spec.Services {
		app.Spec.Services = append(app.Spec.Services, Service{
			Name:   s.Name,
			Type:   s.Type,
			Size:   s.Size,
			Scopes: copyStrings(s.Scopes),
		})
	}

	app.applyDefaults()
	if len(errs) > 0 {
		return app, errors.Join(errs...)
	}
	return app, nil
}

// ToSpec convierte una Application (ya parseada, con o sin defaults) al spec
// estructurado. Los scopes salen en orden estable: dev, staging, prod, pr, y
// cualquier otro nombre después, alfabéticamente. Las listas nunca son nil
// para que el JSON tenga [] y no null.
func ToSpec(app *Application) ApplicationSpec {
	spec := ApplicationSpec{
		Name:        app.Metadata.Name,
		Team:        app.Metadata.Team,
		Tier:        app.Metadata.Tier,
		Description: app.Metadata.Description,
		Type:        app.Spec.Type,
		Runtime:     app.Spec.Runtime,
		Port:        app.Spec.Port,
		Healthcheck: app.Spec.Healthcheck,
		Schedule:    app.Spec.Schedule,
		Scopes:      []SpecScope{},
		Parameters:  []SpecParameter{},
		Services:    []SpecService{},
	}
	for _, name := range SortedScopeNames(app) {
		sc := app.Spec.Scopes[name]
		var as *Autoscaling
		if sc.Autoscaling != nil {
			cp := *sc.Autoscaling
			as = &cp
		}
		spec.Scopes = append(spec.Scopes, SpecScope{
			Name: name, Compute: sc.Compute, Size: sc.Size, Replicas: sc.Replicas, Strategy: sc.Strategy,
			Domain: sc.Domain, Autoscaling: as, TTL: sc.TTL,
		})
	}
	for _, p := range app.Spec.Parameters {
		sp := SpecParameter{Name: p.Name, Scopes: nonNil(copyStrings(p.Scopes))}
		switch {
		case p.Secret:
			sp.Source = SourceSecret
		case p.FromService != "":
			sp.Source = SourceService
			sp.Service = p.FromService
			sp.Key = p.Key
		default:
			sp.Source = SourceValue
			sp.Value = p.Value
		}
		spec.Parameters = append(spec.Parameters, sp)
	}
	for _, s := range app.Spec.Services {
		spec.Services = append(spec.Services, SpecService{
			Name: s.Name, Type: s.Type, Size: s.Size, Scopes: nonNil(copyStrings(s.Scopes)),
		})
	}
	return spec
}

// SortedScopeNames devuelve los nombres de spec.scopes en el orden estable
// dev, staging, prod, pr; los nombres desconocidos van al final, alfabéticamente.
func SortedScopeNames(app *Application) []string {
	names := make([]string, 0, len(app.Spec.Scopes))
	for n := range app.Spec.Scopes {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		oi, oki := scopeOrder[names[i]]
		oj, okj := scopeOrder[names[j]]
		switch {
		case oki && okj:
			return oi < oj
		case oki != okj:
			return oki
		default:
			return names[i] < names[j]
		}
	})
	return names
}

// MarshalManifest serializa la Application como rampa.yaml canónico, con la
// cabecera ManifestHeader, indentación de 2 espacios y los scopes en el orden
// de SortedScopeNames. El resultado se vuelve a leer con Parse sin cambios.
func MarshalManifest(app *Application) ([]byte, error) {
	var root yaml.Node
	if err := root.Encode(app); err != nil {
		return nil, fmt.Errorf("serializando el manifest: %w", err)
	}
	if scopes := mappingValue(mappingValue(&root, "spec"), "scopes"); scopes != nil && scopes.Kind == yaml.MappingNode {
		reorderMapping(scopes, SortedScopeNames(app))
	}
	var buf bytes.Buffer
	buf.WriteString(ManifestHeader)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, fmt.Errorf("serializando el manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("serializando el manifest: %w", err)
	}
	return buf.Bytes(), nil
}

// mappingValue devuelve el nodo valor de una clave en un mapping, o nil.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// reorderMapping reordena los pares clave/valor de un mapping según order.
// Las claves que no están en order conservan su posición relativa al final.
func reorderMapping(n *yaml.Node, order []string) {
	pairs := map[string][2]*yaml.Node{}
	var rest [][2]*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		pairs[n.Content[i].Value] = [2]*yaml.Node{n.Content[i], n.Content[i+1]}
	}
	seen := map[string]bool{}
	var out []*yaml.Node
	for _, k := range order {
		if p, ok := pairs[k]; ok {
			out = append(out, p[0], p[1])
			seen[k] = true
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if !seen[n.Content[i].Value] {
			rest = append(rest, [2]*yaml.Node{n.Content[i], n.Content[i+1]})
		}
	}
	for _, p := range rest {
		out = append(out, p[0], p[1])
	}
	n.Content = out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
