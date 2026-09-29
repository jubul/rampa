package kube

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"

	"rampa.dev/pkg/protocol"
)

const (
	testApp = "checkout-api"
	testNS  = "checkout-api-dev"
)

var selectorLabels = map[string]string{
	"app.kubernetes.io/name":     testApp,
	"app.kubernetes.io/instance": testNS,
}

// deploymentJSON es un Deployment como lo renderiza el chart: labels propias,
// selector y template que el agente no debe tocar.
func deploymentJSON(replicas int) map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":   testApp,
			"labels": map[string]any{"app.kubernetes.io/name": testApp, "helm.sh/chart": "rampa-app-0.1.0"},
		},
		"spec": map[string]any{
			"replicas": replicas,
			"selector": map[string]any{"matchLabels": toAnyMap(selectorLabels)},
			"template": map[string]any{
				"metadata": map[string]any{"labels": toAnyMap(selectorLabels)},
				"spec": map[string]any{
					"containers": []any{map[string]any{"name": "app", "image": "registry/rampa/checkout-api:sha-1234567"}},
				},
			},
		},
	}
}

func serviceJSON(name string) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"selector": toAnyMap(selectorLabels),
			"ports":    []any{map[string]any{"port": 80, "targetPort": "http"}},
		},
	}
}

func deployCmd(t *testing.T, deploymentID string, objects ...any) *protocol.DeployCommand {
	t.Helper()
	cmd := &protocol.DeployCommand{
		DeploymentID: deploymentID,
		App:          testApp,
		Scope:        "dev",
		Namespace:    testNS,
		Labels: map[string]string{
			LabelApp:          testApp,
			"rampa.dev/scope": "dev",
			LabelDeployment:   deploymentID,
		},
		TimeoutSeconds: 5,
	}
	for _, o := range objects {
		cmd.Objects = append(cmd.Objects, mustJSON(t, o))
	}
	return cmd
}

// healthyDeployment es el Deployment ya reconciliado que el fake conserva (status y generation).
func healthyDeployment(deploymentID string, replicas int64) *runtime.Object {
	obj := unstructuredObj(gvkDeployment, testNS, testApp, map[string]string{LabelApp: testApp, LabelDeployment: deploymentID}, map[string]any{
		"spec": map[string]any{"replicas": replicas, "selector": map[string]any{"matchLabels": toAnyMap(selectorLabels)}},
		"status": map[string]any{
			"observedGeneration": int64(1),
			"replicas":           replicas,
			"updatedReplicas":    replicas,
			"readyReplicas":      replicas,
			"availableReplicas":  replicas,
		},
	})
	obj.SetGeneration(1)
	var ro runtime.Object = obj
	return &ro
}

func TestDeployAppliesWithSSAAndLabels(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil)
	// El Deployment ya existe sano: así el apply "termina" enseguida y podemos mirar todo lo demás.
	if err := f.dyn.Tracker().Add(*healthyDeployment("dep-1", 2)); err != nil {
		t.Fatal(err)
	}
	cmd := deployCmd(t, "dep-2", deploymentJSON(2), serviceJSON(testApp))

	status, err := f.client.Deploy(context.Background(), cmd, f.report)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if status.Phase != protocol.PhaseHealthy || status.Kind != "Deployment" || status.Ready != 2 {
		t.Fatalf("status inesperado: %+v", status)
	}

	if len(f.applies) != 2 {
		t.Fatalf("esperaba 2 applies, hubo %d", len(f.applies))
	}
	for _, a := range f.applies {
		if a.PatchType != types.ApplyPatchType {
			t.Errorf("%s: patch type %q, esperaba apply", a.Name, a.PatchType)
		}
		if a.Options.FieldManager != FieldManager {
			t.Errorf("%s: field manager %q", a.Name, a.Options.FieldManager)
		}
		if a.Options.Force == nil || !*a.Options.Force {
			t.Errorf("%s: force debería ser true", a.Name)
		}
		if a.Namespace != testNS {
			t.Errorf("%s: namespace %q", a.Name, a.Namespace)
		}
		meta := a.Object["metadata"].(map[string]any)
		lbls := meta["labels"].(map[string]any)
		for k, v := range cmd.Labels {
			if lbls[k] != v {
				t.Errorf("%s: falta label %s=%s (tiene %v)", a.Name, k, v, lbls)
			}
		}
		if a.Resource.Resource == "deployments" {
			// Las labels propias del chart se conservan y el selector no se toca.
			if lbls["helm.sh/chart"] != "rampa-app-0.1.0" {
				t.Errorf("se perdió una label del chart: %v", lbls)
			}
			sel := a.Object["spec"].(map[string]any)["selector"].(map[string]any)["matchLabels"].(map[string]any)
			if len(sel) != 2 || sel[LabelDeployment] != nil {
				t.Errorf("el selector fue modificado: %v", sel)
			}
			tpl := a.Object["spec"].(map[string]any)["template"].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)
			if tpl[LabelDeployment] != nil {
				t.Errorf("el template fue modificado: %v", tpl)
			}
		}
	}

	ns, err := f.dyn.Tracker().Get(gvrNamespaces, "", testNS)
	if err != nil {
		t.Fatalf("el namespace no se creó: %v", err)
	}
	if got := ns.(interface{ GetLabels() map[string]string }).GetLabels(); got["rampa.dev/scope"] != "dev" || got[LabelApp] != testApp {
		t.Errorf("labels del namespace: %v", got)
	}

	if got := len(f.reasons("Applied")); got != 2 {
		t.Errorf("esperaba 2 eventos Applied, hubo %d", got)
	}
	if got := len(f.reasons("Healthy")); got != 1 {
		t.Errorf("esperaba 1 evento Healthy, hubo %d", got)
	}
}

func TestDeployPrunesOldObjectsAndKeepsNew(t *testing.T) {
	old := unstructuredObj(managedKinds[3], testNS, testApp+"-preview", map[string]string{LabelApp: testApp, LabelDeployment: "dep-1"}, nil)
	otherApp := unstructuredObj(managedKinds[3], testNS, "otra", map[string]string{LabelApp: "otra-app", LabelDeployment: "x"}, nil)
	oldIngress := unstructuredObj(managedKinds[4], testNS, testApp, map[string]string{LabelApp: testApp, LabelDeployment: "dep-1"}, nil)
	f := newFixture(t, fixedMapper(), []runtime.Object{old, otherApp, oldIngress, *healthyDeployment("dep-1", 1)})

	cmd := deployCmd(t, "dep-2", deploymentJSON(1), serviceJSON(testApp))
	if _, err := f.client.Deploy(context.Background(), cmd, f.report); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if _, err := f.get(t, managedKinds[3], testNS, testApp+"-preview"); !apierrors.IsNotFound(err) {
		t.Errorf("el Service viejo debería haberse borrado, err=%v", err)
	}
	if _, err := f.get(t, managedKinds[4], testNS, testApp); !apierrors.IsNotFound(err) {
		t.Errorf("el Ingress viejo debería haberse borrado, err=%v", err)
	}
	if _, err := f.get(t, managedKinds[3], testNS, testApp); err != nil {
		t.Errorf("el Service nuevo no debería borrarse: %v", err)
	}
	if _, err := f.get(t, managedKinds[3], testNS, "otra"); err != nil {
		t.Errorf("el Service de otra app no se toca: %v", err)
	}
	dep, err := f.get(t, gvkDeployment, testNS, testApp)
	if err != nil {
		t.Fatalf("el Deployment nuevo desapareció: %v", err)
	}
	if dep.GetLabels()[LabelDeployment] != "dep-2" {
		t.Errorf("el Deployment quedó con deployment %q", dep.GetLabels()[LabelDeployment])
	}
	pruned := f.reasons("Pruned")
	if len(pruned) != 2 {
		t.Fatalf("esperaba 2 eventos Pruned, hubo %d: %+v", len(pruned), pruned)
	}
}

func TestDeployPruneSkipsKindsNotInstalled(t *testing.T) {
	// Sin ExternalSecret ni Rollout en el cluster: prune no debe fallar.
	f := newFixture(t, fixedMapper("ExternalSecret", "Rollout"), []runtime.Object{*healthyDeployment("dep-1", 1)})
	cmd := deployCmd(t, "dep-2", deploymentJSON(1))
	if _, err := f.client.Deploy(context.Background(), cmd, f.report); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
}

func TestDeployFailsClearlyWhenCRDMissing(t *testing.T) {
	f := newFixture(t, fixedMapper("Rollout"), nil)
	rollout := map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Rollout",
		"metadata":   map[string]any{"name": testApp},
		"spec":       map[string]any{"replicas": 1},
	}
	cmd := deployCmd(t, "dep-1", rollout)
	_, err := f.client.Deploy(context.Background(), cmd, f.report)
	if err == nil {
		t.Fatal("esperaba error por CRD faltante")
	}
	var missing *MissingKindError
	if !errors.As(err, &missing) {
		t.Fatalf("esperaba MissingKindError, fue %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "Argo Rollouts") {
		t.Errorf("mensaje poco claro: %v", err)
	}
}

func TestDeployRejectsClusterScopedObjects(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil)
	cr := map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRole",
		"metadata":   map[string]any{"name": "todo"},
	}
	_, err := f.client.Deploy(context.Background(), deployCmd(t, "dep-1", cr), f.report)
	if err == nil || !strings.Contains(err.Error(), "recurso de cluster") {
		t.Fatalf("esperaba rechazo de objeto de cluster, err=%v", err)
	}
}

func TestDeployWaitsUntilDeploymentHealthy(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil)
	cmd := deployCmd(t, "dep-1", deploymentJSON(2))

	// El "controller": después de un rato completa el status del Deployment.
	go func() {
		time.Sleep(40 * time.Millisecond)
		for {
			obj, err := f.get(t, gvkDeployment, testNS, testApp)
			if err != nil {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			obj = obj.DeepCopy()
			obj.SetGeneration(1)
			_ = setStatus(obj, map[string]any{
				"observedGeneration": int64(1), "replicas": int64(2), "updatedReplicas": int64(2),
				"readyReplicas": int64(1), "availableReplicas": int64(1),
			})
			plural := f.applies[0].Resource
			_ = f.dyn.Tracker().Update(plural, obj, testNS)
			time.Sleep(30 * time.Millisecond)
			obj = obj.DeepCopy()
			_ = setStatus(obj, map[string]any{
				"observedGeneration": int64(1), "replicas": int64(2), "updatedReplicas": int64(2),
				"readyReplicas": int64(2), "availableReplicas": int64(2),
			})
			_ = f.dyn.Tracker().Update(plural, obj, testNS)
			return
		}
	}()

	status, err := f.client.Deploy(context.Background(), cmd, f.report)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if status.Phase != protocol.PhaseHealthy || status.Ready != 2 || status.Desired != 2 {
		t.Fatalf("status: %+v", status)
	}
	if status.Image != "registry/rampa/checkout-api:sha-1234567" {
		t.Errorf("imagen: %q", status.Image)
	}
	progressing := f.reasons("Progressing")
	if len(progressing) < 2 {
		t.Fatalf("esperaba al menos 2 eventos Progressing (0/2 y 1/2), hubo %d: %+v", len(progressing), progressing)
	}
	if !strings.Contains(progressing[0].Message, "0/2") {
		t.Errorf("primer Progressing: %q", progressing[0].Message)
	}
}

// crashLoopPod arma un pod del selector en CrashLoopBackOff con el pod-template-hash dado.
func crashLoopPod(name, hash string, created time.Time) *corev1.Pod {
	lbls := map[string]string{}
	for k, v := range selectorLabels {
		lbls[k] = v
	}
	if hash != "" {
		lbls[labelPodTemplateHash] = hash
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS, Labels: lbls, CreationTimestamp: metav1.NewTime(created)},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "app",
				RestartCount: 4,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
					Reason: "CrashLoopBackOff", Message: "back-off 40s restarting failed container",
				}},
			}},
		},
	}
}

// replicaSet arma un ReplicaSet del Deployment de la app con su revisión y pod-template-hash.
func replicaSet(hash, revision string) *appsv1.ReplicaSet {
	lbls := map[string]string{labelPodTemplateHash: hash}
	for k, v := range selectorLabels {
		lbls[k] = v
	}
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: testApp + "-" + hash, Namespace: testNS, Labels: lbls,
			Annotations:     map[string]string{annotationRevision: revision},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: testApp}},
		},
	}
}

// reconciledDeployment es un Deployment con la revisión dada ya observada por el
// controller pero sin réplicas listas.
func reconciledDeployment(revision string) *unstructured.Unstructured {
	dep := unstructuredObj(gvkDeployment, testNS, testApp, map[string]string{LabelApp: testApp, LabelDeployment: "dep-1"}, map[string]any{
		"spec":   map[string]any{"replicas": int64(1), "selector": map[string]any{"matchLabels": toAnyMap(selectorLabels)}},
		"status": map[string]any{"observedGeneration": int64(1), "replicas": int64(1), "updatedReplicas": int64(1)},
	})
	dep.SetGeneration(1)
	dep.SetAnnotations(map[string]string{annotationRevision: revision})
	return dep
}

func TestDeployFailsFastOnCrashLoopBackOff(t *testing.T) {
	pod := crashLoopPod(testApp+"-abc", "abc", time.Now())
	// Deployment ya reconciliado, con su ReplicaSet de la revisión actual, pero sin réplicas listas.
	dep := reconciledDeployment("2")
	f := newFixture(t, fixedMapper(), []runtime.Object{dep}, pod, replicaSet("abc", "2"))

	cmd := deployCmd(t, "dep-1", deploymentJSON(1))
	cmd.TimeoutSeconds = 30
	start := time.Now()
	status, err := f.client.Deploy(context.Background(), cmd, f.report)
	if err == nil {
		t.Fatal("esperaba fallo por CrashLoopBackOff")
	}
	var unhealthy *UnhealthyError
	if !errors.As(err, &unhealthy) {
		t.Fatalf("esperaba UnhealthyError, fue %T: %v", err, err)
	}
	if unhealthy.Reason != "CrashLoopBackOff" {
		t.Errorf("razón %q", unhealthy.Reason)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("tardó %s: debería fallar antes del timeout", time.Since(start))
	}
	if status == nil || status.Phase != protocol.PhaseFailed {
		t.Errorf("status final: %+v", status)
	}
	unhealthyEvents := f.reasons("Unhealthy")
	if len(unhealthyEvents) != 1 {
		t.Fatalf("esperaba exactamente 1 evento Unhealthy (no se repite si la razón no cambia), hubo %d", len(unhealthyEvents))
	}
	if !strings.Contains(unhealthyEvents[0].Message, "CrashLoopBackOff") || unhealthyEvents[0].Object != "Pod/"+pod.Name {
		t.Errorf("evento Unhealthy: %+v", unhealthyEvents[0])
	}
	failed := f.reasons("CrashLoopBackOff")
	if len(failed) != 1 || failed[0].Phase != protocol.PhaseFailed {
		t.Errorf("esperaba un evento final failed con la razón, hubo %+v", failed)
	}
}

// makeHealthyLater simula al controller: después de delay completa el status del
// Deployment como sano.
func makeHealthyLater(t *testing.T, f *fixture, delay time.Duration) {
	t.Helper()
	go func() {
		time.Sleep(delay)
		obj, err := f.get(t, gvkDeployment, testNS, testApp)
		if err != nil {
			return
		}
		obj = obj.DeepCopy()
		_ = setStatus(obj, map[string]any{
			"observedGeneration": int64(1), "replicas": int64(1), "updatedReplicas": int64(1),
			"readyReplicas": int64(1), "availableReplicas": int64(1),
		})
		plural, _ := meta.UnsafeGuessKindToResource(gvkDeployment)
		_ = f.dyn.Tracker().Update(plural, obj, testNS)
	}()
}

func TestDeployIgnoresCrashLoopingPodsOfOldRevision(t *testing.T) {
	// Tres pods de la revisión anterior en CrashLoopBackOff (lo que el deploy
	// viene a arreglar) y el pod nuevo todavía bajando la imagen.
	oldPods := []runtime.Object{
		crashLoopPod(testApp+"-old-1", "old", time.Now().Add(-time.Hour)),
		crashLoopPod(testApp+"-old-2", "old", time.Now().Add(-time.Hour)),
		crashLoopPod(testApp+"-old-3", "old", time.Now().Add(-time.Hour)),
	}
	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: testApp + "-new-1", Namespace: testNS, Labels: replicaSet("new", "2").Labels},
		Status: corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{{
			Name: "app", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
		}}},
	}
	typed := append(append([]runtime.Object{}, oldPods...), newPod, replicaSet("old", "1"), replicaSet("new", "2"))
	f := newFixture(t, fixedMapper(), []runtime.Object{reconciledDeployment("2")}, typed...)
	// Muchos polls con los pods viejos rotos antes de que el nuevo esté listo.
	makeHealthyLater(t, f, 100*time.Millisecond)

	cmd := deployCmd(t, "dep-1", deploymentJSON(1))
	cmd.TimeoutSeconds = 30
	status, err := f.client.Deploy(context.Background(), cmd, f.report)
	if err != nil {
		t.Fatalf("los pods viejos en CrashLoopBackOff no deberían fallar el deploy nuevo: %v", err)
	}
	if status.Phase != protocol.PhaseHealthy {
		t.Errorf("status: %+v", status)
	}
	if got := f.reasons("Unhealthy"); len(got) != 0 {
		t.Errorf("no debería haber eventos Unhealthy por pods viejos: %+v", got)
	}
}

func TestDeployDoesNotCountStrikesUntilRevisionIsKnown(t *testing.T) {
	// El controller todavía no creó el ReplicaSet nuevo: solo existe el viejo,
	// con sus pods rotos. Sin revisión conocida no se cuentan strikes.
	pods := []runtime.Object{crashLoopPod(testApp+"-old-1", "old", time.Now().Add(-time.Hour)), replicaSet("old", "1")}
	f := newFixture(t, fixedMapper(), []runtime.Object{reconciledDeployment("2")}, pods...)
	makeHealthyLater(t, f, 100*time.Millisecond)

	cmd := deployCmd(t, "dep-1", deploymentJSON(1))
	cmd.TimeoutSeconds = 30
	if _, err := f.client.Deploy(context.Background(), cmd, f.report); err != nil {
		t.Fatalf("sin ReplicaSet nuevo no hay que fallar por pods viejos: %v", err)
	}
}

func TestDeployFallsBackToCreationTimeWithoutReplicaSetAccess(t *testing.T) {
	// Sin permiso para listar ReplicaSets: los pods anteriores al deploy se
	// ignoran y los nuevos siguen contando.
	oldPod := crashLoopPod(testApp+"-old", "", time.Now().Add(-time.Hour))
	f := newFixture(t, fixedMapper(), []runtime.Object{reconciledDeployment("2")}, oldPod)
	f.typed.PrependReactor("list", "replicasets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "replicasets"}, "", errors.New("sin permiso"))
	})
	makeHealthyLater(t, f, 100*time.Millisecond)
	cmd := deployCmd(t, "dep-1", deploymentJSON(1))
	cmd.TimeoutSeconds = 30
	if _, err := f.client.Deploy(context.Background(), cmd, f.report); err != nil {
		t.Fatalf("el pod viejo no debería contar: %v", err)
	}

	// Un pod nuevo roto sí falla rápido.
	newPod := crashLoopPod(testApp+"-new", "", time.Now().Add(time.Minute))
	f2 := newFixture(t, fixedMapper(), []runtime.Object{reconciledDeployment("2")}, newPod)
	f2.typed.PrependReactor("list", "replicasets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "replicasets"}, "", errors.New("sin permiso"))
	})
	var unhealthy *UnhealthyError
	if _, err := f2.client.Deploy(context.Background(), cmd, f2.report); !errors.As(err, &unhealthy) {
		t.Fatalf("esperaba UnhealthyError por el pod nuevo, fue %v", err)
	}
}

func TestDeployTimesOut(t *testing.T) {
	dep := unstructuredObj(gvkDeployment, testNS, testApp, nil, map[string]any{
		"spec":   map[string]any{"replicas": int64(3), "selector": map[string]any{"matchLabels": toAnyMap(selectorLabels)}},
		"status": map[string]any{"observedGeneration": int64(1), "readyReplicas": int64(1)},
	})
	dep.SetGeneration(1)
	f := newFixture(t, fixedMapper(), []runtime.Object{dep})
	cmd := deployCmd(t, "dep-1", deploymentJSON(3))
	cmd.TimeoutSeconds = 1

	status, err := f.client.Deploy(context.Background(), cmd, f.report)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("esperaba ErrTimeout, fue %v", err)
	}
	if status == nil || status.Ready != 1 || status.Desired != 3 {
		t.Errorf("status al vencer: %+v", status)
	}
}

func TestDeployCronJobIsHealthyOnApply(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil)
	cron := map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "CronJob",
		"metadata":   map[string]any{"name": testApp},
		"spec": map[string]any{
			"schedule": "*/5 * * * *",
			"jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "app", "image": "img:1"}},
			}}}},
		},
	}
	status, err := f.client.Deploy(context.Background(), deployCmd(t, "dep-1", cron), f.report)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if status.Kind != "CronJob" || status.Phase != protocol.PhaseHealthy || status.Image != "img:1" {
		t.Errorf("status: %+v", status)
	}
}

func TestDeployValidatesCommand(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil)
	cases := []*protocol.DeployCommand{
		nil,
		{App: testApp, DeploymentID: "d", Objects: []json.RawMessage{[]byte("{}")}},
		{Namespace: testNS, DeploymentID: "d", Objects: []json.RawMessage{[]byte("{}")}},
		{Namespace: testNS, App: testApp, Objects: []json.RawMessage{[]byte("{}")}},
		{Namespace: testNS, App: testApp, DeploymentID: "d"},
	}
	for i, c := range cases {
		if _, err := f.client.Deploy(context.Background(), c, nil); err == nil {
			t.Errorf("caso %d: esperaba error de validación", i)
		}
	}
	if _, err := f.client.Deploy(context.Background(), deployCmd(t, "d", map[string]any{"kind": "Service"}), nil); err == nil || !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("objeto sin apiVersion: %v", err)
	}
}

func setStatus(obj interface{ UnstructuredContent() map[string]any }, status map[string]any) error {
	obj.UnstructuredContent()["status"] = status
	return nil
}
