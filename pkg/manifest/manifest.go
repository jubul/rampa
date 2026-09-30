// Package manifest carga, completa y valida el rampa.yaml: el contrato entre
// una application y la plataforma. Ver docs/modelo.md.
package manifest

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	APIVersion = "rampa.dev/v1"
	Kind       = "Application"
	// PRScopeKey es la clave de la plantilla de scopes efímeros dentro de spec.scopes.
	PRScopeKey = "pr"
)

// Computes son los motores donde corre un scope (ADR 0004): kubernetes es un
// cluster (administrado o una máquina de un nodo con k3s); serverless es la
// misma imagen como función (AWS Lambda, ola dos). Los efímeros heredan el de pr.
const (
	ComputeKubernetes = "kubernetes"
	ComputeServerless = "serverless"
)

var (
	Types           = []string{"api", "worker", "cron", "static"}
	Runtimes        = []string{"go", "node", "python", "static"}
	Sizes           = []string{"S", "M", "L", "XL"}
	Strategies      = []string{"rolling", "canary", "bluegreen"}
	Computes        = []string{ComputeKubernetes, ComputeServerless}
	ServiceTypes    = []string{"postgres", "redis", "sqs", "s3", "dynamodb"}
	PermanentScopes = []string{"dev", "staging", "prod"}

	nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)
	envRe  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	prRe   = regexp.MustCompile(`^pr-[0-9]+$`)
	cronRe = regexp.MustCompile(`^\S+\s+\S+\s+\S+\s+\S+\s+\S+$`)
)

type Application struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

type Metadata struct {
	Name        string `yaml:"name"`
	Team        string `yaml:"team"`
	Tier        int    `yaml:"tier"`
	Description string `yaml:"description,omitempty"`
}

type Spec struct {
	Type        string           `yaml:"type"`
	Runtime     string           `yaml:"runtime"`
	Port        int              `yaml:"port,omitempty"`
	Healthcheck string           `yaml:"healthcheck,omitempty"`
	Schedule    string           `yaml:"schedule,omitempty"`
	Scopes      map[string]Scope `yaml:"scopes"`
	Parameters  []Parameter      `yaml:"parameters"`
	Services    []Service        `yaml:"services"`
}

type Scope struct {
	// Compute es el motor: kubernetes (default) o serverless.
	Compute     string       `yaml:"compute,omitempty"`
	Size        string       `yaml:"size"`
	Replicas    int          `yaml:"replicas"`
	Strategy    string       `yaml:"strategy,omitempty"`
	Domain      string       `yaml:"domain,omitempty"`
	Autoscaling *Autoscaling `yaml:"autoscaling,omitempty"`
	TTL         string       `yaml:"ttl,omitempty"`
}

// Autoscaling lleva tags json además de yaml porque viaja tal cual dentro de
// ApplicationSpec (spec.go), el formato estructurado que usa la web.
type Autoscaling struct {
	Min int `yaml:"min" json:"min"`
	Max int `yaml:"max" json:"max"`
	CPU int `yaml:"cpu" json:"cpu"`
}

// Parameter es una variable de entorno con exactamente un origen:
// value (plano), secret (Secrets Manager) o fromService (credencial de un service).
type Parameter struct {
	Name        string   `yaml:"name"`
	Value       string   `yaml:"value,omitempty"`
	Secret      bool     `yaml:"secret,omitempty"`
	FromService string   `yaml:"fromService,omitempty"`
	Key         string   `yaml:"key,omitempty"`
	Scopes      []string `yaml:"scopes,omitempty"`
}

type Service struct {
	Name   string   `yaml:"name"`
	Type   string   `yaml:"type"`
	Size   string   `yaml:"size"`
	Scopes []string `yaml:"scopes,omitempty"`
}

// Load lee y parsea un rampa.yaml aplicando defaults. No valida.
func Load(path string) (*Application, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no pude leer %s: %w", path, err)
	}
	return Parse(b)
}

// Parse parsea el contenido de un rampa.yaml aplicando defaults. No valida.
func Parse(b []byte) (*Application, error) {
	var app Application
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&app); err != nil {
		return nil, fmt.Errorf("rampa.yaml inválido: %w", err)
	}
	app.applyDefaults()
	return &app, nil
}

// LoadValid es Load más Validate, con los errores agregados en uno.
func LoadValid(path string) (*Application, error) {
	app, err := Load(path)
	if err != nil {
		return nil, err
	}
	if errs := app.Validate(); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = "  - " + e.Error()
		}
		return nil, fmt.Errorf("%s tiene %d error(es):\n%s", path, len(errs), strings.Join(msgs, "\n"))
	}
	return app, nil
}

func (a *Application) applyDefaults() {
	if a.Metadata.Tier == 0 {
		a.Metadata.Tier = 2
	}
	if a.Spec.Type == "" {
		a.Spec.Type = "api"
	}
	if a.Spec.Type == "static" && a.Spec.Runtime == "" {
		a.Spec.Runtime = "static"
	}
	if a.Spec.Type == "api" || a.Spec.Type == "static" {
		if a.Spec.Port == 0 {
			a.Spec.Port = 8080
		}
		if a.Spec.Healthcheck == "" {
			if a.Spec.Type == "static" {
				a.Spec.Healthcheck = "/"
			} else {
				a.Spec.Healthcheck = "/health"
			}
		}
	}
	for name, s := range a.Spec.Scopes {
		if s.Compute == "" {
			s.Compute = ComputeKubernetes
		}
		if s.Size == "" {
			s.Size = "S"
		}
		if s.Replicas == 0 {
			s.Replicas = 1
		}
		if s.Strategy == "" {
			s.Strategy = "rolling"
		}
		if s.Autoscaling != nil && s.Autoscaling.CPU == 0 {
			s.Autoscaling.CPU = 70
		}
		if name == PRScopeKey && s.TTL == "" {
			s.TTL = "72h"
		}
		a.Spec.Scopes[name] = s
	}
	for i, svc := range a.Spec.Services {
		if svc.Size == "" {
			a.Spec.Services[i].Size = "S"
		}
	}
}

// Validate devuelve todos los problemas encontrados, no solo el primero.
func (a *Application) Validate() []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if a.APIVersion != APIVersion {
		add("apiVersion debe ser %q, es %q", APIVersion, a.APIVersion)
	}
	if a.Kind != Kind {
		add("kind debe ser %q, es %q", Kind, a.Kind)
	}
	if !nameRe.MatchString(a.Metadata.Name) {
		add("metadata.name %q inválido: minúsculas, números y guiones, máximo 40 caracteres", a.Metadata.Name)
	}
	if !nameRe.MatchString(a.Metadata.Team) {
		add("metadata.team %q inválido: minúsculas, números y guiones", a.Metadata.Team)
	}
	if a.Metadata.Tier < 1 || a.Metadata.Tier > 3 {
		add("metadata.tier debe ser 1, 2 o 3, es %d", a.Metadata.Tier)
	}
	if !contains(Types, a.Spec.Type) {
		add("spec.type %q inválido. Opciones: %s", a.Spec.Type, strings.Join(Types, ", "))
	}
	if a.Spec.Runtime != "" && !contains(Runtimes, a.Spec.Runtime) {
		add("spec.runtime %q inválido. Opciones: %s", a.Spec.Runtime, strings.Join(Runtimes, ", "))
	}
	switch a.Spec.Type {
	case "api", "static":
		if a.Spec.Port < 1 || a.Spec.Port > 65535 {
			add("spec.port %d fuera de rango", a.Spec.Port)
		}
		if !strings.HasPrefix(a.Spec.Healthcheck, "/") {
			add("spec.healthcheck %q debe empezar con /", a.Spec.Healthcheck)
		}
	case "cron":
		if !cronRe.MatchString(strings.TrimSpace(a.Spec.Schedule)) {
			add("spec.schedule %q debe ser una expresión cron de 5 campos", a.Spec.Schedule)
		}
	}
	if a.Spec.Type != "cron" && a.Spec.Schedule != "" {
		add("spec.schedule solo aplica a type cron")
	}

	if len(a.Spec.Scopes) == 0 {
		add("spec.scopes: declará al menos un scope (dev, staging, prod o pr)")
	}
	hasPermanent := false
	for name, s := range a.Spec.Scopes {
		if name != PRScopeKey && !contains(PermanentScopes, name) {
			add("spec.scopes.%s: nombre inválido. Opciones: %s, %s", name, strings.Join(PermanentScopes, ", "), PRScopeKey)
		}
		if contains(PermanentScopes, name) {
			hasPermanent = true
		}
		if !contains(Computes, s.Compute) {
			add("spec.scopes.%s.compute %q inválido. Opciones: %s", name, s.Compute, strings.Join(Computes, ", "))
		}
		if !contains(Sizes, s.Size) {
			add("spec.scopes.%s.size %q inválido. Opciones: %s", name, s.Size, strings.Join(Sizes, ", "))
		}
		if s.Replicas < 1 {
			add("spec.scopes.%s.replicas debe ser >= 1", name)
		}
		if !contains(Strategies, s.Strategy) {
			add("spec.scopes.%s.strategy %q inválida. Opciones: %s", name, s.Strategy, strings.Join(Strategies, ", "))
		}
		if a.Spec.Type == "cron" && s.Strategy != "rolling" {
			add("spec.scopes.%s: un cron no tiene strategy", name)
		}
		if s.Domain != "" && (a.Spec.Type == "worker" || a.Spec.Type == "cron") {
			add("spec.scopes.%s.domain no aplica a type %s", name, a.Spec.Type)
		}
		if name == PRScopeKey && s.Domain != "" {
			add("spec.scopes.pr.domain: los scopes efímeros usan la URL por defecto")
		}
		if as := s.Autoscaling; as != nil {
			if as.Min < 1 || as.Max < as.Min {
				add("spec.scopes.%s.autoscaling: se necesita 1 <= min <= max", name)
			}
			if as.CPU < 1 || as.CPU > 100 {
				add("spec.scopes.%s.autoscaling.cpu debe estar entre 1 y 100", name)
			}
			if a.Spec.Type == "cron" {
				add("spec.scopes.%s: un cron no autoescala", name)
			}
		}
	}
	if !hasPermanent && len(a.Spec.Scopes) > 0 {
		add("spec.scopes: hace falta al menos un scope permanente además de pr")
	}

	svcNames := map[string]Service{}
	for i, svc := range a.Spec.Services {
		p := fmt.Sprintf("spec.services[%d]", i)
		if !nameRe.MatchString(svc.Name) {
			add("%s.name %q inválido", p, svc.Name)
		}
		if _, dup := svcNames[svc.Name]; dup {
			add("%s.name %q repetido", p, svc.Name)
		}
		svcNames[svc.Name] = svc
		if !contains(ServiceTypes, svc.Type) {
			add("%s.type %q inválido. Opciones: %s", p, svc.Type, strings.Join(ServiceTypes, ", "))
		}
		if !contains(Sizes, svc.Size) {
			add("%s.size %q inválido. Opciones: %s", p, svc.Size, strings.Join(Sizes, ", "))
		}
		for _, sc := range svc.Scopes {
			if sc == PRScopeKey {
				add("%s.scopes: los scopes efímeros no reciben services gestionados", p)
			} else if _, ok := a.Spec.Scopes[sc]; !ok {
				add("%s.scopes: %q no está declarado en spec.scopes", p, sc)
			}
		}
	}

	for i, prm := range a.Spec.Parameters {
		p := fmt.Sprintf("spec.parameters[%d]", i)
		if err := ValidateParameterName(prm.Name); err != nil {
			add("%s.name %v", p, err)
		}
		sources := 0
		if prm.Value != "" {
			sources++
		}
		if prm.Secret {
			sources++
		}
		if prm.FromService != "" {
			sources++
		}
		if sources != 1 {
			add("%s (%s): exactamente uno de value, secret: true o fromService", p, prm.Name)
		}
		if prm.FromService != "" {
			if _, ok := svcNames[prm.FromService]; !ok {
				add("%s (%s): fromService %q no existe en spec.services", p, prm.Name, prm.FromService)
			}
			if prm.Key == "" {
				add("%s (%s): fromService necesita key (url, host, password...)", p, prm.Name)
			}
		} else if prm.Key != "" {
			add("%s (%s): key solo aplica con fromService", p, prm.Name)
		}
		for _, sc := range prm.Scopes {
			if _, ok := a.Spec.Scopes[sc]; !ok {
				add("%s (%s).scopes: %q no está declarado en spec.scopes", p, prm.Name, sc)
			}
		}
	}
	return errs
}

// ValidateParameterName aplica la regla de los nombres de parameters (y de los
// secretos de aplicación, que son la misma cosa): MAYUSCULAS_CON_GUION_BAJO y
// sin los nombres reservados por la plataforma (RAMPA_* y PORT). Devuelve el
// error listo para anteponerle el campo: `%q inválido: ...`.
func ValidateParameterName(name string) error {
	if !envRe.MatchString(name) {
		return fmt.Errorf("%q inválido: MAYUSCULAS_CON_GUION_BAJO", name)
	}
	if strings.HasPrefix(name, "RAMPA_") || name == "PORT" {
		return fmt.Errorf("%q está reservado por la plataforma", name)
	}
	return nil
}

// IsEphemeral dice si el nombre corresponde a un scope efímero (pr-<n>).
func IsEphemeral(scope string) bool { return prRe.MatchString(scope) }

// Namespace es el namespace de Kubernetes de un scope: <app>-<scope>.
func (a *Application) Namespace(scope string) string { return a.Metadata.Name + "-" + scope }

// ResolveScope devuelve la configuración de un scope concreto. Para pr-<n> usa la plantilla pr.
func (a *Application) ResolveScope(name string) (Scope, bool, error) {
	if name == PRScopeKey {
		return Scope{}, false, errors.New("\"pr\" es la plantilla; el scope concreto es pr-<número>")
	}
	if s, ok := a.Spec.Scopes[name]; ok {
		return s, false, nil
	}
	if IsEphemeral(name) {
		s, ok := a.Spec.Scopes[PRScopeKey]
		if !ok {
			return Scope{}, true, fmt.Errorf("el scope %s es efímero pero el rampa.yaml no declara la plantilla spec.scopes.pr", name)
		}
		return s, true, nil
	}
	declared := make([]string, 0, len(a.Spec.Scopes))
	for n := range a.Spec.Scopes {
		declared = append(declared, n)
	}
	sort.Strings(declared)
	return Scope{}, false, fmt.Errorf("scope %q no declarado. Declarados: %s", name, strings.Join(declared, ", "))
}

// ServicesFor devuelve los services disponibles en un scope. Los efímeros no reciben ninguno.
func (a *Application) ServicesFor(scope string) []Service {
	if IsEphemeral(scope) {
		return nil
	}
	var out []Service
	for _, s := range a.Spec.Services {
		if len(s.Scopes) == 0 || contains(s.Scopes, scope) {
			out = append(out, s)
		}
	}
	return out
}

// ParametersFor resuelve los parameters de un scope: los genéricos primero y
// los específicos encima. Un fromService solo aplica donde el service existe;
// los que se descartan se devuelven en dropped para avisar.
func (a *Application) ParametersFor(scope string) (params []Parameter, dropped []Parameter) {
	ephemeral := IsEphemeral(scope)
	applies := func(p Parameter) bool {
		if len(p.Scopes) == 0 {
			return true
		}
		for _, s := range p.Scopes {
			if s == scope || (ephemeral && s == PRScopeKey) {
				return true
			}
		}
		return false
	}
	available := map[string]bool{}
	for _, s := range a.ServicesFor(scope) {
		available[s.Name] = true
	}

	var order []string
	byName := map[string]Parameter{}
	set := func(p Parameter) {
		if _, seen := byName[p.Name]; !seen {
			order = append(order, p.Name)
		}
		byName[p.Name] = p
	}
	for _, p := range a.Spec.Parameters {
		if len(p.Scopes) == 0 {
			set(p)
		}
	}
	for _, p := range a.Spec.Parameters {
		if len(p.Scopes) > 0 && applies(p) {
			set(p)
		}
	}
	for _, name := range order {
		p := byName[name]
		if p.FromService != "" && !available[p.FromService] {
			dropped = append(dropped, p)
			continue
		}
		params = append(params, p)
	}
	return params, dropped
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
