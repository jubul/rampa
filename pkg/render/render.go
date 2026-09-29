// Package render traduce un rampa.yaml más un scope a los values del chart rampa-app.
package render

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"rampa.dev/pkg/manifest"
)

// Platform es el platform.yaml que vive en rampa-deploys: lo que el dev no
// tiene por qué saber (registry, clusters, dominios).
type Platform struct {
	Registry string             `yaml:"registry"`
	GitHub   GitHub             `yaml:"github,omitempty"`
	Clusters map[string]Cluster `yaml:"clusters"`
}

type GitHub struct {
	Org string `yaml:"org,omitempty"`
}

type Cluster struct {
	Domain       string   `yaml:"domain"`
	IngressClass string   `yaml:"ingressClass,omitempty"`
	SecretStore  string   `yaml:"secretStore,omitempty"`
	Scopes       []string `yaml:"scopes"` // patrones glob: dev, staging, "pr-*"
}

// LoadPlatform lee un platform.yaml.
func LoadPlatform(p string) (*Platform, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("no pude leer %s: %w", p, err)
	}
	var plat Platform
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&plat); err != nil {
		return nil, fmt.Errorf("%s inválido: %w", p, err)
	}
	return &plat, nil
}

// LoadPlatformOptional devuelve nil sin error si la ruta está vacía.
func LoadPlatformOptional(p string) (*Platform, error) {
	if p == "" {
		return nil, nil
	}
	return LoadPlatform(p)
}

// ClusterFor devuelve el cluster cuyo patrón de scopes matchea. Recorre en orden alfabético.
func (p *Platform) ClusterFor(scope string) (string, *Cluster) {
	if p == nil {
		return "", nil
	}
	names := make([]string, 0, len(p.Clusters))
	for n := range p.Clusters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := p.Clusters[n]
		for _, pat := range c.Scopes {
			if ok, _ := path.Match(pat, scope); ok {
				return n, &c
			}
		}
	}
	return "", nil
}

// Options controla el render.
type Options struct {
	Image    string // tag ("sha-abc1234") o referencia completa ("registry/rampa/app:tag")
	Platform *Platform
	Registry string   // pisa al de Platform
	Domain   string   // pisa al del cluster
	Cluster  *Cluster // si se pasa, gana sobre Platform: es lo que usa la API con los datos del cluster real
}

// Values es el esquema exacto que consume charts/rampa-app. El orden de los
// campos es el orden en que se serializan.
type Values struct {
	App struct {
		Name    string `yaml:"name"`
		Team    string `yaml:"team"`
		Tier    int    `yaml:"tier"`
		Type    string `yaml:"type"`
		Runtime string `yaml:"runtime,omitempty"`
	} `yaml:"app"`
	Scope struct {
		Name      string `yaml:"name"`
		Ephemeral bool   `yaml:"ephemeral"`
		TTL       string `yaml:"ttl,omitempty"`
	} `yaml:"scope"`
	Image struct {
		Repository string `yaml:"repository"`
		Tag        string `yaml:"tag"`
	} `yaml:"image"`
	Port        int          `yaml:"port,omitempty"`
	Healthcheck string       `yaml:"healthcheck,omitempty"`
	Schedule    string       `yaml:"schedule,omitempty"`
	Replicas    int          `yaml:"replicas"`
	Size        string       `yaml:"size"`
	Strategy    string       `yaml:"strategy"`
	Autoscaling *Autoscaling `yaml:"autoscaling,omitempty"`
	Ingress     struct {
		Enabled bool   `yaml:"enabled"`
		Host    string `yaml:"host,omitempty"`
	} `yaml:"ingress"`
	Env     []EnvVar    `yaml:"env"`
	Secrets []SecretRef `yaml:"secrets"`
	Cluster struct {
		Domain       string `yaml:"domain,omitempty"`
		IngressClass string `yaml:"ingressClass,omitempty"`
		SecretStore  string `yaml:"secretStore,omitempty"`
	} `yaml:"cluster"`
}

type Autoscaling struct {
	Enabled bool `yaml:"enabled"`
	Min     int  `yaml:"min"`
	Max     int  `yaml:"max"`
	CPU     int  `yaml:"cpu"`
}

type EnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type SecretRef struct {
	Name      string `yaml:"name"`
	RemoteKey string `yaml:"remoteKey"`
	Property  string `yaml:"property,omitempty"`
}

// Build construye los values del chart para un scope. Devuelve avisos no fatales.
func Build(app *manifest.Application, scopeName string, o Options) (*Values, []string, error) {
	var warns []string
	sc, ephemeral, err := app.ResolveScope(scopeName)
	if err != nil {
		return nil, nil, err
	}

	v := &Values{}
	v.App.Name = app.Metadata.Name
	v.App.Team = app.Metadata.Team
	v.App.Tier = app.Metadata.Tier
	v.App.Type = app.Spec.Type
	v.App.Runtime = app.Spec.Runtime
	v.Scope.Name = scopeName
	v.Scope.Ephemeral = ephemeral
	if ephemeral {
		v.Scope.TTL = sc.TTL
	}

	// Imagen
	registry := o.Registry
	if registry == "" && o.Platform != nil {
		registry = o.Platform.Registry
	}
	repo, tag := splitImage(o.Image)
	if repo == "" {
		if registry == "" {
			warns = append(warns, "sin registry (platform.yaml o --registry): image.repository queda como rampa/"+app.Metadata.Name)
			repo = "rampa/" + app.Metadata.Name
		} else {
			repo = strings.TrimSuffix(registry, "/") + "/rampa/" + app.Metadata.Name
		}
	}
	if tag == "" {
		warns = append(warns, "sin --image: uso el tag latest, que no es reproducible")
		tag = "latest"
	}
	v.Image.Repository = repo
	v.Image.Tag = tag

	// Runtime
	if app.Spec.Type == "api" || app.Spec.Type == "static" {
		v.Port = app.Spec.Port
		v.Healthcheck = app.Spec.Healthcheck
		v.Ingress.Enabled = true
		v.Ingress.Host = sc.Domain
	}
	v.Schedule = app.Spec.Schedule
	v.Replicas = sc.Replicas
	v.Size = sc.Size
	v.Strategy = sc.Strategy
	if sc.Autoscaling != nil {
		v.Autoscaling = &Autoscaling{Enabled: true, Min: sc.Autoscaling.Min, Max: sc.Autoscaling.Max, CPU: sc.Autoscaling.CPU}
		v.Replicas = sc.Autoscaling.Min
	}

	// Cluster
	_, cluster := o.Platform.ClusterFor(scopeName)
	if o.Cluster != nil {
		cluster = o.Cluster
	}
	if cluster != nil {
		v.Cluster.Domain = cluster.Domain
		v.Cluster.IngressClass = cluster.IngressClass
		v.Cluster.SecretStore = cluster.SecretStore
	} else if o.Platform != nil {
		warns = append(warns, fmt.Sprintf("ningún cluster de platform.yaml matchea el scope %s; el chart usa su dominio por defecto", scopeName))
	}
	if o.Domain != "" {
		v.Cluster.Domain = o.Domain
	}

	// Parameters
	params, dropped := app.ParametersFor(scopeName)
	env := []EnvVar{}
	secrets := []SecretRef{}
	for _, p := range params {
		switch {
		case p.Secret:
			secrets = append(secrets, SecretRef{
				Name:      p.Name,
				RemoteKey: fmt.Sprintf("rampa/%s/%s/%s", app.Metadata.Name, scopeName, p.Name),
			})
		case p.FromService != "":
			secrets = append(secrets, SecretRef{
				Name:      p.Name,
				RemoteKey: fmt.Sprintf("rampa/services/%s/%s/%s", app.Metadata.Name, p.FromService, scopeName),
				Property:  p.Key,
			})
		default:
			env = append(env, EnvVar{Name: p.Name, Value: p.Value})
		}
	}
	for _, d := range dropped {
		warns = append(warns, fmt.Sprintf("parameter %s omitido en %s: el service %s no existe en ese scope", d.Name, scopeName, d.FromService))
	}
	v.Env = env
	v.Secrets = secrets
	return v, warns, nil
}

// URL devuelve la URL pública de un scope renderizado, o "" si no expone HTTP.
func URL(v *Values) string {
	if !v.Ingress.Enabled {
		return ""
	}
	host := v.Ingress.Host
	if host == "" {
		domain := v.Cluster.Domain
		if domain == "" {
			domain = "apps.example.internal" // mismo default que el chart
		}
		host = fmt.Sprintf("%s-%s.%s", v.App.Name, v.Scope.Name, domain)
	}
	return "https://" + host
}

// splitImage separa "registry/rampa/app:tag" en repo y tag. Un string sin "/" es solo un tag.
func splitImage(image string) (repo, tag string) {
	if image == "" {
		return "", ""
	}
	if !strings.Contains(image, "/") {
		return "", image
	}
	lastSlash := strings.LastIndex(image, "/")
	if i := strings.LastIndex(image, ":"); i > lastSlash {
		return image[:i], image[i+1:]
	}
	return image, ""
}
