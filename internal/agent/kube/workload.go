package kube

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"rampa.dev/pkg/protocol"
)

// Observaciones consecutivas con pods rotos antes de declarar el deploy fallido.
const unhealthyStrikes = 3

// Errores consecutivos leyendo el workload antes de dar el deploy por fallido.
// Un 503 aislado del API server (upgrade del control plane, rate limiting) no
// tiene que tirar un deploy que en el cluster sigue avanzando.
const maxObserveErrors = 5

const (
	// Labels y annotations con los que Deployment y Rollout marcan la revisión
	// de sus pods. Solo los pods de la revisión actual cuentan para fallar rápido:
	// los de la revisión anterior en CrashLoopBackOff son justamente lo que el
	// deploy nuevo viene a arreglar.
	labelPodTemplateHash      = "pod-template-hash"
	labelRolloutsTemplateHash = "rollouts-pod-template-hash"
	annotationRevision        = "deployment.kubernetes.io/revision"
)

// ErrWorkloadNotFound indica que en el namespace no hay Deployment, Rollout ni
// CronJob con el nombre de la app.
var ErrWorkloadNotFound = errors.New("no encontré un Deployment, Rollout ni CronJob")

// podFilter dice si un pod pertenece a la revisión actual del workload.
type podFilter func(pod *corev1.Pod) bool

// Razones de contenedores que indican que el pod no va a levantar solo.
var fatalWaitingReasons = map[string]bool{
	"ImagePullBackOff":           true,
	"ErrImagePull":               true,
	"CrashLoopBackOff":           true,
	"CreateContainerConfigError": true,
}

// observation es una foto del workload.
type observation struct {
	status   protocol.WorkloadStatus
	healthy  bool
	failed   bool
	selector map[string]string // selector de pods, vacío para CronJob
	// current filtra los pods de la revisión actual. nil significa que todavía
	// no se puede saber cuál es (el controller no reconcilió): en ese caso no se
	// cuentan problemas de pods.
	current podFilter
}

// podProblem es un contenedor en un estado del que no va a salir solo.
type podProblem struct {
	Reason  string
	Pod     string
	Message string
}

// Status devuelve el estado actual del workload de una app en un namespace.
func (c *Client) Status(ctx context.Context, cmd *protocol.StatusCommand) (*protocol.WorkloadStatus, error) {
	if cmd == nil || cmd.App == "" || cmd.Namespace == "" {
		return nil, errors.New("el comando status necesita app y namespace")
	}
	obs, err := c.observe(ctx, cmd.Namespace, cmd.App, time.Time{})
	if err != nil {
		return nil, err
	}
	st := obs.status
	switch {
	case obs.failed:
		st.Phase = protocol.PhaseFailed
	case obs.healthy:
		st.Phase = protocol.PhaseHealthy
	default:
		st.Phase = protocol.PhaseProgressing
		if len(obs.selector) > 0 && obs.current != nil {
			problems, perr := c.podProblems(ctx, cmd.Namespace, obs.selector, obs.current)
			if perr == nil && len(problems) > 0 {
				st.Phase = protocol.PhaseFailed
				st.Message = summarizeProblems(problems)
			}
		}
	}
	return &st, nil
}

// waitHealthy observa el workload hasta healthy, failed o que venza ctx. since
// es cuándo empezó el deploy: se usa para distinguir pods viejos de nuevos si no
// se puede leer la revisión del controller.
func (c *Client) waitHealthy(ctx context.Context, namespace, app string, since time.Time, report Reporter) (*protocol.WorkloadStatus, error) {
	ticker := time.NewTicker(c.pollInterval())
	defer ticker.Stop()

	var (
		last          *protocol.WorkloadStatus
		lastProgress  string
		lastReasons   string
		strikes       int
		observeErrors int
	)
	for {
		obs, err := c.observe(ctx, namespace, app, since)
		if err != nil {
			if ctx.Err() != nil {
				return last, fmt.Errorf("%w: %v", ErrTimeout, err)
			}
			observeErrors++
			if observeErrors >= maxObserveErrors {
				return last, fmt.Errorf("%d lecturas seguidas del workload fallaron: %w", observeErrors, err)
			}
			c.logger().Warn("no pude leer el workload, reintento en el próximo tick", "namespace", namespace, "app", app, "intento", observeErrors, "err", err)
			select {
			case <-ctx.Done():
				return last, fmt.Errorf("%w: %v", ErrTimeout, err)
			case <-ticker.C:
			}
			continue
		}
		observeErrors = 0
		st := obs.status
		last = &st
		if obs.failed {
			st.Phase = protocol.PhaseFailed
			report(protocol.PhaseFailed, "Degraded", st.Kind+"/"+app, st.Message)
			return &st, &UnhealthyError{Reason: "Degraded", Message: st.Message}
		}
		if obs.healthy {
			st.Phase = protocol.PhaseHealthy
			if st.Message == "" {
				st.Message = fmt.Sprintf("%d/%d réplicas listas", st.Ready, st.Desired)
			}
			return &st, nil
		}
		st.Phase = protocol.PhaseProgressing

		progress := fmt.Sprintf("%d/%d", st.Ready, st.Desired)
		if progress != lastProgress {
			lastProgress = progress
			report(protocol.PhaseProgressing, "Progressing", st.Kind+"/"+app, fmt.Sprintf("%s réplicas listas", progress))
		}

		// Sin revisión conocida no se cuentan strikes: no sabemos qué pods son
		// del deploy nuevo y los viejos pueden estar rotos a propósito.
		if len(obs.selector) > 0 && obs.current != nil {
			problems, perr := c.podProblems(ctx, namespace, obs.selector, obs.current)
			if perr != nil {
				c.logger().Warn("no pude listar los pods del workload", "namespace", namespace, "app", app, "err", perr)
			} else if len(problems) > 0 {
				strikes++
				reasons := reasonKey(problems)
				if reasons != lastReasons {
					lastReasons = reasons
					for _, p := range problems {
						report(protocol.PhaseProgressing, "Unhealthy", "Pod/"+p.Pod, p.Reason+": "+p.Message)
					}
				}
				if strikes >= unhealthyStrikes {
					msg := summarizeProblems(problems)
					st.Phase = protocol.PhaseFailed
					st.Message = msg
					report(protocol.PhaseFailed, problems[0].Reason, st.Kind+"/"+app, msg)
					return &st, &UnhealthyError{Reason: problems[0].Reason, Message: msg}
				}
			} else {
				strikes = 0
				lastReasons = ""
			}
		}

		select {
		case <-ctx.Done():
			st.Message = fmt.Sprintf("%s réplicas listas al vencer el plazo", progress)
			return &st, fmt.Errorf("%w (%s réplicas listas)", ErrTimeout, progress)
		case <-ticker.C:
		}
	}
}

// observe busca el workload llamado app (Deployment, Rollout o CronJob, el que
// exista) y resuelve qué pods son de la revisión actual. since es el inicio del
// deploy (cero para status) y solo se usa como fallback.
func (c *Client) observe(ctx context.Context, namespace, app string, since time.Time) (*observation, error) {
	for _, gvk := range []schema.GroupVersionKind{gvkDeployment, gvkRollout, gvkCronJob} {
		m, err := c.mapping(gvk)
		if err != nil {
			var missing *MissingKindError
			if errors.As(err, &missing) {
				continue
			}
			return nil, err
		}
		obj, err := c.Dynamic.Resource(m.Resource).Namespace(namespace).Get(ctx, app, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("no pude leer %s/%s: %w", gvk.Kind, app, err)
		}
		switch gvk.Kind {
		case "Deployment":
			obs := observeDeployment(obj)
			obs.current = c.deploymentPodFilter(ctx, obj, since)
			return obs, nil
		case "Rollout":
			return observeRollout(obj), nil
		default:
			return observeCronJob(obj), nil
		}
	}
	return nil, fmt.Errorf("%w llamado %q en el namespace %s", ErrWorkloadNotFound, app, namespace)
}

// deploymentPodFilter devuelve el filtro de pods de la revisión actual de un
// Deployment: los del ReplicaSet cuya annotation deployment.kubernetes.io/revision
// coincide con la del Deployment, identificados por su label pod-template-hash.
// Devuelve nil si el controller todavía no reconcilió la generación actual o no
// creó el ReplicaSet nuevo. Si no se pueden listar ReplicaSets (por ejemplo por
// falta de RBAC), cae a considerar solo los pods creados desde since.
func (c *Client) deploymentPodFilter(ctx context.Context, dep *unstructured.Unstructured, since time.Time) podFilter {
	observedGen, _ := nestedInt64(dep, "status", "observedGeneration")
	if observedGen < dep.GetGeneration() {
		return nil
	}
	revision := dep.GetAnnotations()[annotationRevision]
	if revision == "" {
		return nil
	}
	selector := nestedStringMap(dep, "spec", "selector", "matchLabels")
	if len(selector) == 0 {
		return nil
	}
	rsList, err := c.Typed.AppsV1().ReplicaSets(dep.GetNamespace()).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set(selector)).String(),
	})
	if err != nil {
		c.logger().Warn("no pude listar los ReplicaSets del Deployment; considero solo pods creados desde el inicio del deploy",
			"namespace", dep.GetNamespace(), "app", dep.GetName(), "err", err)
		return createdSince(since)
	}
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if rs.Annotations[annotationRevision] != revision || !ownedByDeployment(rs, dep) {
			continue
		}
		hash := rs.Labels[labelPodTemplateHash]
		if hash == "" {
			continue
		}
		return withLabel(labelPodTemplateHash, hash)
	}
	return nil
}

// ownedByDeployment dice si el ReplicaSet tiene al Deployment como dueño.
func ownedByDeployment(rs *appsv1.ReplicaSet, dep *unstructured.Unstructured) bool {
	for _, ref := range rs.OwnerReferences {
		if ref.Kind != "Deployment" || ref.Name != dep.GetName() {
			continue
		}
		if dep.GetUID() == "" || ref.UID == dep.GetUID() {
			return true
		}
	}
	return false
}

func withLabel(key, value string) podFilter {
	return func(pod *corev1.Pod) bool { return pod.Labels[key] == value }
}

// createdSince acepta los pods creados a partir de since. Con since en cero
// acepta todos.
func createdSince(since time.Time) podFilter {
	return func(pod *corev1.Pod) bool {
		return since.IsZero() || !pod.CreationTimestamp.Time.Before(since)
	}
}

func observeDeployment(obj *unstructured.Unstructured) *observation {
	desired, hasSpec := nestedInt32(obj, "spec", "replicas")
	if !hasSpec {
		desired, _ = nestedInt32(obj, "status", "replicas")
	}
	observedGen, _ := nestedInt64(obj, "status", "observedGeneration")
	updated, _ := nestedInt32(obj, "status", "updatedReplicas")
	ready, _ := nestedInt32(obj, "status", "readyReplicas")
	available, _ := nestedInt32(obj, "status", "availableReplicas")

	healthy := observedGen >= obj.GetGeneration() &&
		updated == desired && ready == desired && available == desired
	return &observation{
		status: protocol.WorkloadStatus{
			Kind:      "Deployment",
			Desired:   desired,
			Ready:     ready,
			Updated:   updated,
			Available: available,
			Image:     containerImage(obj, "spec", "template", "spec", "containers"),
		},
		healthy:  healthy,
		selector: nestedStringMap(obj, "spec", "selector", "matchLabels"),
	}
}

func observeRollout(obj *unstructured.Unstructured) *observation {
	desired, hasSpec := nestedInt32(obj, "spec", "replicas")
	if !hasSpec {
		desired, _ = nestedInt32(obj, "status", "replicas")
	}
	updated, _ := nestedInt32(obj, "status", "updatedReplicas")
	ready, _ := nestedInt32(obj, "status", "readyReplicas")
	available, _ := nestedInt32(obj, "status", "availableReplicas")
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	message, _, _ := unstructured.NestedString(obj.Object, "status", "message")
	if message == "" {
		message = phase
	}
	// Argo Rollouts guarda observedGeneration como string. Hasta que el
	// controller no reconcilie la generación actual, phase es del rollout
	// anterior y no vale ni para Healthy ni para Degraded.
	reconciled := rolloutObservedGeneration(obj) >= obj.GetGeneration()
	if !reconciled {
		message = "esperando a que Argo Rollouts reconcilie la nueva revisión"
	}
	obs := &observation{
		status: protocol.WorkloadStatus{
			Kind:      "Rollout",
			Desired:   desired,
			Ready:     ready,
			Updated:   updated,
			Available: available,
			Image:     containerImage(obj, "spec", "template", "spec", "containers"),
			Message:   message,
		},
		healthy:  reconciled && phase == "Healthy",
		failed:   reconciled && phase == "Degraded",
		selector: nestedStringMap(obj, "spec", "selector", "matchLabels"),
	}
	if hash, _, _ := unstructured.NestedString(obj.Object, "status", "currentPodHash"); reconciled && hash != "" {
		obs.current = withLabel(labelRolloutsTemplateHash, hash)
	}
	return obs
}

// rolloutObservedGeneration lee status.observedGeneration de un Rollout, que
// puede venir como string o como número. Si falta o no se entiende, devuelve 0.
func rolloutObservedGeneration(obj *unstructured.Unstructured) int64 {
	if n, ok := nestedInt64(obj, "status", "observedGeneration"); ok {
		return n
	}
	str, found, err := unstructured.NestedString(obj.Object, "status", "observedGeneration")
	if err != nil || !found {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(str), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// Un CronJob está sano en cuanto existe: no hay réplicas que esperar.
func observeCronJob(obj *unstructured.Unstructured) *observation {
	schedule, _, _ := unstructured.NestedString(obj.Object, "spec", "schedule")
	return &observation{
		status: protocol.WorkloadStatus{
			Kind:    "CronJob",
			Image:   containerImage(obj, "spec", "jobTemplate", "spec", "template", "spec", "containers"),
			Message: "cron programado: " + schedule,
		},
		healthy: true,
	}
}

// podProblems lista los pods del selector que pasan el filtro de revisión y
// devuelve los contenedores en estados fatales (ImagePullBackOff,
// CrashLoopBackOff, OOMKilled repetido...). Con current nil considera todos.
func (c *Client) podProblems(ctx context.Context, namespace string, selector map[string]string, current podFilter) ([]podProblem, error) {
	pods, err := c.Typed.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set(selector)).String(),
	})
	if err != nil {
		return nil, err
	}
	var out []podProblem
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		if current != nil && !current(pod) {
			continue
		}
		statuses := append(append([]corev1.ContainerStatus{}, pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...)
		for _, cs := range statuses {
			if w := cs.State.Waiting; w != nil && fatalWaitingReasons[w.Reason] {
				out = append(out, podProblem{Reason: w.Reason, Pod: pod.Name, Message: firstNonEmpty(w.Message, "contenedor "+cs.Name)})
				continue
			}
			oom := false
			if t := cs.State.Terminated; t != nil && t.Reason == "OOMKilled" {
				oom = true
			}
			if t := cs.LastTerminationState.Terminated; t != nil && t.Reason == "OOMKilled" {
				oom = true
			}
			if oom && cs.RestartCount >= 2 {
				out = append(out, podProblem{Reason: "OOMKilled", Pod: pod.Name, Message: fmt.Sprintf("contenedor %s se quedó sin memoria %d veces", cs.Name, cs.RestartCount)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Reason != out[j].Reason {
			return out[i].Reason < out[j].Reason
		}
		return out[i].Pod < out[j].Pod
	})
	return out, nil
}

func reasonKey(problems []podProblem) string {
	parts := make([]string, 0, len(problems))
	for _, p := range problems {
		parts = append(parts, p.Reason+"@"+p.Pod)
	}
	return strings.Join(parts, ",")
}

func summarizeProblems(problems []podProblem) string {
	if len(problems) == 0 {
		return ""
	}
	p := problems[0]
	msg := fmt.Sprintf("pod %s en %s: %s", p.Pod, p.Reason, p.Message)
	if len(problems) > 1 {
		msg += fmt.Sprintf(" (y %d contenedores más con problemas)", len(problems)-1)
	}
	return msg
}

// containerImage devuelve la imagen del contenedor "app" o, si no está, del primero.
func containerImage(obj *unstructured.Unstructured, fields ...string) string {
	containers, found, err := unstructured.NestedSlice(obj.Object, fields...)
	if err != nil || !found || len(containers) == 0 {
		return ""
	}
	first := ""
	for _, c := range containers {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		img, _ := m["image"].(string)
		if first == "" {
			first = img
		}
		if name, _ := m["name"].(string); name == "app" {
			return img
		}
	}
	return first
}

func nestedInt64(obj *unstructured.Unstructured, fields ...string) (int64, bool) {
	v, found, err := unstructured.NestedFieldNoCopy(obj.Object, fields...)
	if err != nil || !found {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func nestedInt32(obj *unstructured.Unstructured, fields ...string) (int32, bool) {
	n, ok := nestedInt64(obj, fields...)
	return int32(n), ok
}

func nestedStringMap(obj *unstructured.Unstructured, fields ...string) map[string]string {
	m, found, err := unstructured.NestedStringMap(obj.Object, fields...)
	if err != nil || !found {
		return nil
	}
	return m
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
