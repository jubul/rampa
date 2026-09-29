package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	"rampa.dev/pkg/protocol"
)

// Deploy ejecuta un DeployCommand completo: namespace, apply, prune y espera.
// Devuelve el último estado observado del workload. Si el workload no llega a
// estar sano en TimeoutSeconds el error envuelve ErrTimeout; si falla antes,
// el error es *UnhealthyError. Los hitos salen por report.
func (c *Client) Deploy(ctx context.Context, cmd *protocol.DeployCommand, report Reporter) (*protocol.WorkloadStatus, error) {
	if report == nil {
		report = func(protocol.Phase, string, string, string) {}
	}
	if err := validateDeploy(cmd); err != nil {
		return nil, err
	}
	timeout := time.Duration(cmd.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultDeployWait
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()

	lbls := deployLabels(cmd)
	log := c.logger().With("app", cmd.App, "namespace", cmd.Namespace, "deployment", cmd.DeploymentID)

	if err := c.ensureNamespace(ctx, cmd.Namespace, lbls); err != nil {
		return nil, fmt.Errorf("namespace %s: %w", cmd.Namespace, err)
	}
	report(protocol.PhaseApplying, "Namespace", "Namespace/"+cmd.Namespace, "namespace listo")

	for i, raw := range cmd.Objects {
		name, err := c.applyObject(ctx, cmd.Namespace, raw, lbls)
		if err != nil {
			return nil, fmt.Errorf("objeto %d: %w", i+1, err)
		}
		report(protocol.PhaseApplying, "Applied", name, "aplicado con server-side apply")
		log.Debug("objeto aplicado", "object", name)
	}

	if err := c.prune(ctx, cmd.Namespace, cmd.App, cmd.DeploymentID, report); err != nil {
		return nil, fmt.Errorf("prune: %w", err)
	}

	status, err := c.waitHealthy(ctx, cmd.Namespace, cmd.App, started, report)
	if err != nil {
		return status, err
	}
	report(protocol.PhaseHealthy, "Healthy", status.Kind+"/"+cmd.App, status.Message)
	return status, nil
}

func validateDeploy(cmd *protocol.DeployCommand) error {
	switch {
	case cmd == nil:
		return errors.New("el comando deploy está vacío")
	case cmd.Namespace == "":
		return errors.New("el comando deploy no trae namespace")
	case cmd.App == "":
		return errors.New("el comando deploy no trae app")
	case cmd.DeploymentID == "":
		return errors.New("el comando deploy no trae deployment_id")
	case len(cmd.Objects) == 0:
		return errors.New("el comando deploy no trae objetos")
	case isProtectedNamespace(cmd.Namespace):
		return fmt.Errorf("me niego a desplegar en el namespace del sistema %q", cmd.Namespace)
	}
	return nil
}

// deployLabels devuelve los labels del comando garantizando que estén los que
// el prune necesita, aunque la API se los haya olvidado.
func deployLabels(cmd *protocol.DeployCommand) map[string]string {
	out := make(map[string]string, len(cmd.Labels)+2)
	for k, v := range cmd.Labels {
		out[k] = v
	}
	out[LabelApp] = cmd.App
	out[LabelDeployment] = cmd.DeploymentID
	return out
}

// ensureNamespace crea el namespace con los labels o actualiza los labels si ya existe.
func (c *Client) ensureNamespace(ctx context.Context, name string, lbls map[string]string) error {
	nsClient := c.Dynamic.Resource(gvrNamespaces)
	existing, err := nsClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("no pude consultar el namespace: %w", err)
		}
		ns := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]any{
				"name":   name,
				"labels": toAnyMap(lbls),
			},
		}}
		if _, err := nsClient.Create(ctx, ns, metav1.CreateOptions{FieldManager: FieldManager}); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return nil
			}
			return fmt.Errorf("no pude crear el namespace: %w", err)
		}
		return nil
	}
	if existing.GetDeletionTimestamp() != nil {
		return errors.New("el namespace se está borrando; reintentá cuando termine")
	}
	current := existing.GetLabels()
	changed := false
	if current == nil {
		current = map[string]string{}
	}
	for k, v := range lbls {
		if current[k] != v {
			current[k] = v
			changed = true
		}
	}
	if !changed {
		return nil
	}
	existing.SetLabels(current)
	if _, err := nsClient.Update(ctx, existing, metav1.UpdateOptions{FieldManager: FieldManager}); err != nil {
		return fmt.Errorf("no pude actualizar los labels del namespace: %w", err)
	}
	return nil
}

// applyObject agrega los labels al metadata de primer nivel y aplica el objeto
// con server-side apply en el namespace indicado. Devuelve "Kind/nombre".
func (c *Client) applyObject(ctx context.Context, namespace string, raw json.RawMessage, lbls map[string]string) (string, error) {
	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal(raw, &obj.Object); err != nil {
		return "", fmt.Errorf("JSON inválido: %w", err)
	}
	gvk := obj.GroupVersionKind()
	if gvk.Kind == "" || gvk.Version == "" {
		return "", errors.New("el objeto no tiene apiVersion o kind")
	}
	if obj.GetName() == "" {
		return "", fmt.Errorf("el objeto %s no tiene metadata.name", gvk.Kind)
	}
	ref := gvk.Kind + "/" + obj.GetName()

	m, err := c.mapping(gvk)
	if err != nil {
		return ref, err
	}
	if m.Scope.Name() != meta.RESTScopeNameNamespace {
		return ref, fmt.Errorf("%s es un recurso de cluster; el agente solo aplica objetos dentro del namespace del scope", ref)
	}

	// Solo metadata de primer nivel: nunca spec.selector ni spec.template.
	merged := obj.GetLabels()
	if merged == nil {
		merged = map[string]string{}
	}
	for k, v := range lbls {
		merged[k] = v
	}
	obj.SetLabels(merged)
	obj.SetNamespace(namespace)
	// Campos que no tienen sentido en un apply y pueden hacerlo fallar.
	unstructured.RemoveNestedField(obj.Object, "metadata", "resourceVersion")
	unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")
	unstructured.RemoveNestedField(obj.Object, "metadata", "uid")
	unstructured.RemoveNestedField(obj.Object, "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(obj.Object, "status")

	data, err := json.Marshal(obj.Object)
	if err != nil {
		return ref, fmt.Errorf("no pude serializar %s: %w", ref, err)
	}
	_, err = c.Dynamic.Resource(m.Resource).Namespace(namespace).Patch(ctx, obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
		FieldManager: FieldManager,
		Force:        boolPtr(true),
	})
	if err != nil {
		return ref, fmt.Errorf("no pude aplicar %s: %w", ref, err)
	}
	return ref, nil
}

// prune borra, en cada kind gestionado, los objetos de la app cuyo label
// rampa.dev/deployment sea distinto del deployment actual. Los kinds que el
// cluster no tiene se saltean.
func (c *Client) prune(ctx context.Context, namespace, app, deploymentID string, report Reporter) error {
	selector := labels.SelectorFromSet(labels.Set{LabelApp: app}).String()
	var errs []error
	for _, gvk := range managedKinds {
		m, err := c.mapping(gvk)
		if err != nil {
			var missing *MissingKindError
			if errors.As(err, &missing) {
				c.logger().Debug("prune: kind no instalado, se saltea", "kind", gvk.Kind)
				continue
			}
			errs = append(errs, err)
			continue
		}
		res := c.Dynamic.Resource(m.Resource).Namespace(namespace)
		list, err := res.List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			errs = append(errs, fmt.Errorf("no pude listar %s: %w", gvk.Kind, err))
			continue
		}
		for i := range list.Items {
			item := &list.Items[i]
			if item.GetLabels()[LabelDeployment] == deploymentID {
				continue
			}
			ref := gvk.Kind + "/" + item.GetName()
			policy := metav1.DeletePropagationBackground
			err := res.Delete(ctx, item.GetName(), metav1.DeleteOptions{PropagationPolicy: &policy})
			if err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("no pude borrar %s: %w", ref, err))
				continue
			}
			report(protocol.PhaseApplying, "Pruned", ref, fmt.Sprintf("borrado: pertenecía al deployment %q", item.GetLabels()[LabelDeployment]))
		}
	}
	return errors.Join(errs...)
}

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
