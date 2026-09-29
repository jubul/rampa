package kube

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"rampa.dev/pkg/protocol"
)

func TestStatusReportsPhases(t *testing.T) {
	healthy := *healthyDeployment("dep-1", 2)
	f := newFixture(t, fixedMapper(), []runtime.Object{healthy})
	st, err := f.client.Status(context.Background(), &protocol.StatusCommand{App: testApp, Namespace: testNS})
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != protocol.PhaseHealthy || st.Kind != "Deployment" || st.Desired != 2 || st.Available != 2 {
		t.Errorf("healthy: %+v", st)
	}

	// Rollout degradado.
	rollout := unstructuredObj(gvkRollout, testNS, "otra", nil, map[string]any{
		"spec":   map[string]any{"replicas": int64(3)},
		"status": map[string]any{"phase": "Degraded", "message": "ProgressDeadlineExceeded", "readyReplicas": int64(1)},
	})
	f2 := newFixture(t, fixedMapper(), []runtime.Object{rollout})
	st, err = f2.client.Status(context.Background(), &protocol.StatusCommand{App: "otra", Namespace: testNS})
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != protocol.PhaseFailed || st.Kind != "Rollout" || st.Message != "ProgressDeadlineExceeded" {
		t.Errorf("rollout degradado: %+v", st)
	}

	// Sin spec.replicas (autoscaling): usa status.replicas.
	hpaDep := unstructuredObj(gvkDeployment, testNS, "hpa", nil, map[string]any{
		"spec":   map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"a": "b"}}},
		"status": map[string]any{"observedGeneration": int64(0), "replicas": int64(4), "updatedReplicas": int64(4), "readyReplicas": int64(2), "availableReplicas": int64(2)},
	})
	f3 := newFixture(t, fixedMapper(), []runtime.Object{hpaDep})
	st, err = f3.client.Status(context.Background(), &protocol.StatusCommand{App: "hpa", Namespace: testNS})
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != protocol.PhaseProgressing || st.Desired != 4 || st.Ready != 2 {
		t.Errorf("autoscaling: %+v", st)
	}

	// Nada con ese nombre.
	if _, err := f3.client.Status(context.Background(), &protocol.StatusCommand{App: "nada", Namespace: testNS}); err == nil || !strings.Contains(err.Error(), "no encontré") {
		t.Errorf("esperaba error de workload inexistente, fue %v", err)
	}
}

func TestPodProblemsDetectsFatalStates(t *testing.T) {
	mk := func(name string, cs corev1.ContainerStatus) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS, Labels: selectorLabels},
			Status:     corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{cs}},
		}
	}
	pods := []runtime.Object{
		mk("pull", corev1.ContainerStatus{Name: "app", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}),
		mk("cfg", corev1.ContainerStatus{Name: "app", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CreateContainerConfigError", Message: "secret no existe"}}}),
		mk("oom-once", corev1.ContainerStatus{Name: "app", RestartCount: 1, LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"}}}),
		mk("oom-many", corev1.ContainerStatus{Name: "app", RestartCount: 3, LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"}}}),
		mk("ok", corev1.ContainerStatus{Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}),
		mk("starting", corev1.ContainerStatus{Name: "app", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}}),
	}
	f := newFixture(t, fixedMapper(), nil, pods...)
	problems, err := f.client.podProblems(context.Background(), testNS, selectorLabels, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range problems {
		got[p.Pod] = p.Reason
	}
	want := map[string]string{"pull": "ImagePullBackOff", "cfg": "CreateContainerConfigError", "oom-many": "OOMKilled"}
	if len(got) != len(want) {
		t.Fatalf("problemas: %v, esperaba %v", got, want)
	}
	for pod, reason := range want {
		if got[pod] != reason {
			t.Errorf("%s: %q, esperaba %q", pod, got[pod], reason)
		}
	}
}

func TestUndeployDeletesNamespaceAndWaits(t *testing.T) {
	ns := namespaceObj(testNS, map[string]string{LabelApp: testApp})
	f := newFixture(t, fixedMapper(), []runtime.Object{ns})
	if _, err := f.dyn.Tracker().Get(gvrNamespaces, "", testNS); err != nil {
		t.Fatalf("el fixture debería tener el namespace: %v", err)
	}

	err := f.client.Undeploy(context.Background(), &protocol.UndeployCommand{App: testApp, Scope: "dev", Namespace: testNS, Wait: true}, f.report)
	if err != nil {
		t.Fatalf("Undeploy: %v", err)
	}
	if _, err := f.dyn.Tracker().Get(gvrNamespaces, "", testNS); !apierrors.IsNotFound(err) {
		t.Errorf("el namespace debería no existir, err=%v", err)
	}
	if len(f.reasons("Deleted")) != 1 {
		t.Errorf("esperaba un evento Deleted: %+v", f.events)
	}

	// Repetir es idempotente.
	if err := f.client.Undeploy(context.Background(), &protocol.UndeployCommand{App: testApp, Scope: "dev", Namespace: testNS}, nil); err != nil {
		t.Errorf("segundo undeploy: %v", err)
	}
}

func TestUndeployRefusesSuspiciousNamespaces(t *testing.T) {
	// Un namespace con la convención <app>-<scope> pero que no creó el agente
	// para esa app (sin label rampa.dev/app o con otra app).
	foreign := namespaceObj("ajeno-prod", nil)
	otherOwner := namespaceObj("pagos-dev", map[string]string{LabelApp: "otra-app"})
	f := newFixture(t, fixedMapper(), []runtime.Object{foreign, otherOwner})
	cases := []*protocol.UndeployCommand{
		{App: "kube", Scope: "system", Namespace: "kube-system"},
		{App: "kube", Scope: "node-lease", Namespace: "kube-node-lease"},
		{App: "kube", Scope: "loquesea", Namespace: "kube-loquesea"},
		{App: "rampa", Scope: "system", Namespace: "rampa-system"},
		{App: testApp, Scope: "dev", Namespace: "otro-namespace"},
		{App: "", Scope: "", Namespace: "default"},
		{App: "ajeno", Scope: "prod", Namespace: "ajeno-prod"},
		{App: "pagos", Scope: "dev", Namespace: "pagos-dev"},
		nil,
	}
	for i, c := range cases {
		if err := f.client.Undeploy(context.Background(), c, nil); err == nil {
			t.Errorf("caso %d: esperaba rechazo", i)
		}
	}
	for _, name := range []string{"ajeno-prod", "pagos-dev"} {
		if _, err := f.dyn.Tracker().Get(gvrNamespaces, "", name); err != nil {
			t.Errorf("el namespace %s no debería haberse borrado: %v", name, err)
		}
	}
}

func TestDeployRefusesSystemNamespaces(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil)
	for _, ns := range []string{"kube-system", "kube-public", "kube-otro", "default", "rampa-system"} {
		cmd := deployCmd(t, "dep-1", deploymentJSON(1))
		cmd.Namespace = ns
		if _, err := f.client.Deploy(context.Background(), cmd, nil); err == nil || !strings.Contains(err.Error(), "namespace del sistema") {
			t.Errorf("%s: esperaba rechazo, fue %v", ns, err)
		}
		if _, err := f.dyn.Tracker().Get(gvrNamespaces, "", ns); !apierrors.IsNotFound(err) {
			t.Errorf("%s: no debería haberse creado ni tocado, err=%v", ns, err)
		}
	}
	if len(f.applies) != 0 {
		t.Errorf("no debería haberse aplicado nada: %+v", f.applies)
	}
}

func TestObserveRolloutRequiresObservedGeneration(t *testing.T) {
	mk := func(generation int64, observed any, phase string) *unstructured.Unstructured {
		status := map[string]any{"phase": phase, "currentPodHash": "abc123"}
		if observed != nil {
			status["observedGeneration"] = observed
		}
		r := unstructuredObj(gvkRollout, testNS, testApp, nil, map[string]any{
			"spec":   map[string]any{"replicas": int64(2), "selector": map[string]any{"matchLabels": toAnyMap(selectorLabels)}},
			"status": status,
		})
		r.SetGeneration(generation)
		return r
	}
	// Status viejo: la generación observada es menor que la actual.
	stale := observeRollout(mk(3, "2", "Healthy"))
	if stale.healthy || stale.failed || stale.current != nil {
		t.Errorf("con observedGeneration atrasada debería ser progressing sin filtro de pods: %+v", stale)
	}
	staleDegraded := observeRollout(mk(3, "2", "Degraded"))
	if staleDegraded.failed {
		t.Error("un Degraded del rollout anterior no debe fallar el deploy nuevo")
	}
	missing := observeRollout(mk(1, nil, "Healthy"))
	if missing.healthy {
		t.Error("sin observedGeneration no se puede aceptar Healthy")
	}
	// Reconciliado: phase vale, como string o como número.
	for _, observed := range []any{"3", int64(3), "4"} {
		fresh := observeRollout(mk(3, observed, "Healthy"))
		if !fresh.healthy || fresh.failed || fresh.current == nil {
			t.Errorf("observedGeneration=%v: esperaba healthy con filtro de pods: %+v", observed, fresh)
		}
		if !fresh.current(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelRolloutsTemplateHash: "abc123"}}}) ||
			fresh.current(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelRolloutsTemplateHash: "viejo"}}}) {
			t.Error("el filtro debe aceptar solo pods con rollouts-pod-template-hash = status.currentPodHash")
		}
	}
	if !observeRollout(mk(3, "3", "Degraded")).failed {
		t.Error("Degraded reconciliado debe ser failed")
	}
}

func TestWaitHealthyRolloutIgnoresStalePhase(t *testing.T) {
	// El Rollout quedó Healthy de la revisión anterior; el apply sube la generación
	// pero el controller todavía no reconcilió.
	rollout := unstructuredObj(gvkRollout, testNS, testApp, map[string]string{LabelApp: testApp, LabelDeployment: "dep-1"}, map[string]any{
		"spec":   map[string]any{"replicas": int64(1), "selector": map[string]any{"matchLabels": toAnyMap(selectorLabels)}},
		"status": map[string]any{"phase": "Healthy", "observedGeneration": "1", "readyReplicas": int64(1)},
	})
	rollout.SetGeneration(2)
	f := newFixture(t, fixedMapper(), []runtime.Object{rollout})
	plural, _ := meta.UnsafeGuessKindToResource(gvkRollout)

	go func() {
		time.Sleep(60 * time.Millisecond)
		obj, err := f.get(t, gvkRollout, testNS, testApp)
		if err != nil {
			return
		}
		obj = obj.DeepCopy()
		_ = setStatus(obj, map[string]any{"phase": "Paused", "observedGeneration": "2", "readyReplicas": int64(1)})
		_ = f.dyn.Tracker().Update(plural, obj, testNS)
		time.Sleep(30 * time.Millisecond)
		obj = obj.DeepCopy()
		_ = setStatus(obj, map[string]any{"phase": "Healthy", "observedGeneration": "2", "readyReplicas": int64(1), "availableReplicas": int64(1)})
		_ = f.dyn.Tracker().Update(plural, obj, testNS)
	}()

	start := time.Now()
	st, err := f.client.waitHealthy(context.Background(), testNS, testApp, start, f.report)
	if err != nil {
		t.Fatalf("waitHealthy: %v", err)
	}
	if st.Phase != protocol.PhaseHealthy {
		t.Errorf("status: %+v", st)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Error("aceptó el Healthy viejo sin esperar a que el controller reconciliara")
	}
}

func TestWaitHealthyToleratesTransientObserveErrors(t *testing.T) {
	f := newFixture(t, fixedMapper(), []runtime.Object{*healthyDeployment("dep-1", 1)})
	var failures atomic.Int32
	f.dyn.PrependReactor("get", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if failures.Add(1) <= 3 {
			return true, nil, apierrors.NewServiceUnavailable("etcd leader changed")
		}
		return false, nil, nil
	})
	st, err := f.client.waitHealthy(context.Background(), testNS, testApp, time.Now(), f.report)
	if err != nil {
		t.Fatalf("tres 503 seguidos no deberían tirar el deploy: %v", err)
	}
	if st.Phase != protocol.PhaseHealthy {
		t.Errorf("status: %+v", st)
	}
	if failures.Load() < 4 {
		t.Errorf("esperaba reintentos, hubo %d lecturas", failures.Load())
	}
}

func TestWaitHealthyFailsAfterConsecutiveObserveErrors(t *testing.T) {
	f := newFixture(t, fixedMapper(), []runtime.Object{*healthyDeployment("dep-1", 1)})
	var calls atomic.Int32
	f.dyn.PrependReactor("get", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		calls.Add(1)
		return true, nil, apierrors.NewServiceUnavailable("caído")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := f.client.waitHealthy(ctx, testNS, testApp, time.Now(), f.report)
	if err == nil || errors.Is(err, ErrTimeout) {
		t.Fatalf("esperaba error persistente, fue %v", err)
	}
	if !strings.Contains(err.Error(), "caído") || calls.Load() != maxObserveErrors {
		t.Errorf("err=%v lecturas=%d", err, calls.Load())
	}
}

func TestLogsStreamsPodsAndEndsWithEOF(t *testing.T) {
	pods := []runtime.Object{
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNS, Labels: map[string]string{LabelName: testApp}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: testNS, Labels: map[string]string{LabelName: testApp}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "otro", Namespace: testNS, Labels: map[string]string{LabelName: "otra-app"}}},
	}
	f := newFixture(t, fixedMapper(), nil, pods...)

	var chunks []protocol.LogChunk
	err := f.client.Logs(context.Background(), &protocol.LogsCommand{App: testApp, Namespace: testNS, TailLines: 10}, func(c protocol.LogChunk) error {
		chunks = append(chunks, c)
		return nil
	})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("esperaba 2 chunks de datos y 1 EOF, hubo %d: %+v", len(chunks), chunks)
	}
	seen := map[string]bool{}
	for _, c := range chunks[:2] {
		if c.EOF || len(c.Lines) != 1 || c.Lines[0] != "fake logs" || c.Container != "app" {
			t.Errorf("chunk inesperado: %+v", c)
		}
		seen[c.Pod] = true
	}
	if !seen["p1"] || !seen["p2"] || seen["otro"] {
		t.Errorf("pods vistos: %v", seen)
	}
	last := chunks[len(chunks)-1]
	if !last.EOF || len(last.Lines) != 0 {
		t.Errorf("el último chunk debe ser EOF: %+v", last)
	}
}

func TestLogsWithoutPodsSendsOnlyEOF(t *testing.T) {
	f := newFixture(t, fixedMapper(), nil)
	var chunks []protocol.LogChunk
	err := f.client.Logs(context.Background(), &protocol.LogsCommand{App: testApp, Namespace: testNS, Container: "sidecar"}, func(c protocol.LogChunk) error {
		chunks = append(chunks, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || !chunks[0].EOF || chunks[0].Container != "sidecar" {
		t.Errorf("chunks: %+v", chunks)
	}
}

func TestLogsFollowRespectsDuration(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: testNS, Labels: map[string]string{LabelName: testApp}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	f := newFixture(t, fixedMapper(), nil, pod)
	start := time.Now()
	var eof bool
	err := f.client.Logs(context.Background(), &protocol.LogsCommand{App: testApp, Namespace: testNS, Follow: true, DurationSeconds: 1}, func(c protocol.LogChunk) error {
		eof = c.EOF
		return nil
	})
	if err != nil {
		t.Fatalf("Logs follow: %v", err)
	}
	if !eof {
		t.Error("faltó el EOF final")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("tardó demasiado: %s", time.Since(start))
	}
}
