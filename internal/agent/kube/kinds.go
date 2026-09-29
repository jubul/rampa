package kube

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Kinds que el agente gestiona: son los que genera el chart rampa-app y los
// únicos sobre los que hace prune. Un kind que no existe en el cluster (CRD no
// instalada) se saltea en prune y falla con mensaje claro al aplicar.
var managedKinds = []schema.GroupVersionKind{
	{Group: "apps", Version: "v1", Kind: "Deployment"},
	{Group: "argoproj.io", Version: "v1alpha1", Kind: "Rollout"},
	{Group: "batch", Version: "v1", Kind: "CronJob"},
	{Group: "", Version: "v1", Kind: "Service"},
	{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
	{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"},
	{Group: "policy", Version: "v1", Kind: "PodDisruptionBudget"},
	{Group: "", Version: "v1", Kind: "ServiceAccount"},
	{Group: "external-secrets.io", Version: "v1", Kind: "ExternalSecret"},
	{Group: "", Version: "v1", Kind: "ResourceQuota"},
}

var (
	gvkDeployment = managedKinds[0]
	gvkRollout    = managedKinds[1]
	gvkCronJob    = managedKinds[2]

	gvrNamespaces = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
)

// ManagedKinds devuelve una copia de los kinds gestionados (para el README y los tests).
func ManagedKinds() []schema.GroupVersionKind {
	out := make([]schema.GroupVersionKind, len(managedKinds))
	copy(out, managedKinds)
	return out
}

// MissingKindError indica que el cluster no reconoce un kind, normalmente porque
// falta instalar una CRD.
type MissingKindError struct {
	GVK schema.GroupVersionKind
}

func (e *MissingKindError) Error() string {
	switch {
	case e.GVK.Group == "argoproj.io" && e.GVK.Kind == "Rollout":
		return "el cluster no tiene instalado Argo Rollouts, necesario para strategy canary o bluegreen"
	case e.GVK.Group == "external-secrets.io" && e.GVK.Kind == "ExternalSecret":
		return "el cluster no tiene instalado External Secrets Operator, necesario para parameters secretos"
	default:
		return fmt.Sprintf("el cluster no reconoce el kind %s de %s", e.GVK.Kind, e.GVK.GroupVersion().String())
	}
}

// mapping resuelve un GVK a su recurso. Devuelve *MissingKindError si el cluster no lo tiene.
func (c *Client) mapping(gvk schema.GroupVersionKind) (*meta.RESTMapping, error) {
	m, err := c.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		if meta.IsNoMatchError(err) {
			return nil, &MissingKindError{GVK: gvk}
		}
		return nil, fmt.Errorf("no pude resolver el recurso de %s: %w", gvk.String(), err)
	}
	return m, nil
}
