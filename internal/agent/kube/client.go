// Package kube es el brazo del agente sobre Kubernetes: aplica los objetos que
// manda la API con server-side apply, hace prune, observa el workload hasta que
// esté sano, borra namespaces y lee logs. No conoce el chart ni el rampa.yaml:
// recibe JSON ya renderizado.
package kube

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

	"rampa.dev/pkg/protocol"
)

const (
	// FieldManager es el nombre con el que el agente firma sus server-side applies.
	FieldManager = "rampa"

	// LabelApp identifica la application dueña de un objeto.
	LabelApp = "rampa.dev/app"
	// LabelDeployment identifica el deployment que aplicó el objeto. Se usa para prune.
	LabelDeployment = "rampa.dev/deployment"
	// LabelName es el label estándar que el chart pone en los pods.
	LabelName = "app.kubernetes.io/name"

	defaultPollInterval = 2 * time.Second
	defaultDeployWait   = 10 * time.Minute
	// UndeployWait es cuánto espera Undeploy a que el namespace desaparezca cuando Wait es true.
	UndeployWait = 5 * time.Minute
)

// ErrTimeout indica que el workload no llegó a estar sano dentro del plazo.
var ErrTimeout = errors.New("timeout esperando a que el workload esté sano")

// UnhealthyError indica que el workload falló antes del timeout.
type UnhealthyError struct {
	Reason  string
	Message string
}

func (e *UnhealthyError) Error() string {
	if e.Message == "" {
		return e.Reason
	}
	return e.Reason + ": " + e.Message
}

// Reporter recibe los hitos de una operación. object es kind/nombre y puede ir vacío.
type Reporter func(phase protocol.Phase, reason, object, message string)

// Client agrupa lo que el agente necesita de un cluster.
type Client struct {
	Dynamic dynamic.Interface
	Typed   kubernetes.Interface
	Mapper  meta.RESTMapper
	Log     *slog.Logger
	// PollInterval es cada cuánto se observa el workload mientras se espera. Default 2s.
	PollInterval time.Duration
}

// New arma un cliente con la config in-cluster o, si kubeconfig no está vacío, con ese archivo.
// Verifica la conexión pidiendo la versión del servidor.
func New(ctx context.Context, kubeconfig, agentVersion string, log *slog.Logger) (*Client, error) {
	var (
		cfg *rest.Config
		err error
	)
	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("no pude leer el kubeconfig %s: %w", kubeconfig, err)
		}
	} else {
		cfg, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("no estoy dentro de un cluster y RAMPA_KUBECONFIG está vacío: %w", err)
		}
	}
	cfg.UserAgent = "rampa-agent/" + agentVersion
	cfg.QPS = 50
	cfg.Burst = 100

	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("no pude crear el cliente de Kubernetes: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("no pude crear el cliente dinámico: %w", err)
	}
	cached := memory.NewMemCacheClient(typed.Discovery())
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(cached)

	c := &Client{Dynamic: dyn, Typed: typed, Mapper: mapper, Log: log, PollInterval: defaultPollInterval}
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	v, err := c.KubernetesVersion(pingCtx)
	if err != nil {
		return nil, fmt.Errorf("no pude hablar con el API server: %w", err)
	}
	log.Info("conectado a Kubernetes", "version", v, "host", cfg.Host)
	return c, nil
}

// KubernetesVersion devuelve la versión del API server (GitVersion, por ejemplo v1.31.2).
func (c *Client) KubernetesVersion(ctx context.Context) (string, error) {
	var d discovery.DiscoveryInterface = c.Typed.Discovery()
	if dc, ok := d.(discovery.DiscoveryInterfaceWithContext); ok {
		info, err := dc.ServerVersionWithContext(ctx)
		if err != nil {
			return "", err
		}
		return info.GitVersion, nil
	}
	info, err := d.ServerVersion()
	if err != nil {
		return "", err
	}
	return info.GitVersion, nil
}

// NodeCount devuelve cuántos nodos tiene el cluster. Es informativo (heartbeat).
func (c *Client) NodeCount(ctx context.Context) (int, error) {
	nodes, err := c.Typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, err
	}
	return len(nodes.Items), nil
}

func (c *Client) logger() *slog.Logger {
	if c.Log == nil {
		return slog.Default()
	}
	return c.Log
}

func (c *Client) pollInterval() time.Duration {
	if c.PollInterval <= 0 {
		return defaultPollInterval
	}
	return c.PollInterval
}

func boolPtr(b bool) *bool { return &b }

func int64Ptr(n int64) *int64 { return &n }
