package kube

import (
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"rampa.dev/pkg/protocol"
)

// applyRecord es lo que capturamos de cada server-side apply.
type applyRecord struct {
	Resource  schema.GroupVersionResource
	Namespace string
	Name      string
	PatchType types.PatchType
	Options   metav1.PatchOptions
	Object    map[string]any
}

// fixture arma un Client con clientes fake y un RESTMapper fijo.
type fixture struct {
	client  *Client
	dyn     *dynamicfake.FakeDynamicClient
	typed   *k8sfake.Clientset
	mu      sync.Mutex
	applies []applyRecord
	events  []protocol.Event
}

// fixedMapper devuelve un RESTMapper con los kinds gestionados, menos los excluidos.
func fixedMapper(exclude ...string) meta.RESTMapper {
	skip := map[string]bool{}
	for _, k := range exclude {
		skip[k] = true
	}
	var gvs []schema.GroupVersion
	for _, gvk := range managedKinds {
		gvs = append(gvs, gvk.GroupVersion())
	}
	gvs = append(gvs, schema.GroupVersion{Group: "", Version: "v1"})
	m := meta.NewDefaultRESTMapper(gvs)
	for _, gvk := range managedKinds {
		if skip[gvk.Kind] {
			continue
		}
		m.Add(gvk, meta.RESTScopeNamespace)
	}
	m.Add(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}, meta.RESTScopeRoot)
	m.Add(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}, meta.RESTScopeNamespace)
	m.Add(schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}, meta.RESTScopeRoot)
	return m
}

func listKinds() map[schema.GroupVersionResource]string {
	out := map[schema.GroupVersionResource]string{}
	for _, gvk := range managedKinds {
		plural, _ := meta.UnsafeGuessKindToResource(gvk)
		out[plural] = gvk.Kind + "List"
	}
	out[gvrNamespaces] = "NamespaceList"
	return out
}

// newFixture crea el fixture. Los objetos dinámicos iniciales van en dynObjects,
// los pods (tipados) en typedObjects.
func newFixture(t *testing.T, mapper meta.RESTMapper, dynObjects []runtime.Object, typedObjects ...runtime.Object) *fixture {
	t.Helper()
	f := &fixture{}
	scheme := runtime.NewScheme()
	f.dyn = dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds(), dynObjects...)
	f.typed = k8sfake.NewClientset(typedObjects...)

	// El tracker fake no sabe crear objetos con apply: interceptamos el patch,
	// registramos lo que llegó y hacemos upsert conservando status y generation.
	f.dyn.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pa, ok := action.(k8stesting.PatchActionImpl)
		if !ok || pa.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		obj := &unstructured.Unstructured{Object: map[string]any{}}
		if err := json.Unmarshal(pa.GetPatch(), &obj.Object); err != nil {
			return true, nil, err
		}
		f.mu.Lock()
		f.applies = append(f.applies, applyRecord{
			Resource:  pa.GetResource(),
			Namespace: pa.GetNamespace(),
			Name:      pa.GetName(),
			PatchType: pa.GetPatchType(),
			Options:   pa.PatchOptions,
			Object:    obj.Object,
		})
		f.mu.Unlock()

		tracker := f.dyn.Tracker()
		existing, err := tracker.Get(pa.GetResource(), pa.GetNamespace(), pa.GetName())
		if apierrors.IsNotFound(err) {
			return true, obj, tracker.Create(pa.GetResource(), obj, pa.GetNamespace())
		}
		if err != nil {
			return true, nil, err
		}
		old := existing.(*unstructured.Unstructured)
		if st, found, _ := unstructured.NestedFieldNoCopy(old.Object, "status"); found {
			_ = unstructured.SetNestedField(obj.Object, st, "status")
		}
		if gen := old.GetGeneration(); gen != 0 {
			obj.SetGeneration(gen)
		}
		// SSA conserva los campos de otros field managers: las annotations que
		// pone el controller (por ejemplo deployment.kubernetes.io/revision)
		// sobreviven a un apply que no las trae.
		if len(old.GetAnnotations()) > 0 {
			merged := obj.GetAnnotations()
			if merged == nil {
				merged = map[string]string{}
			}
			for k, v := range old.GetAnnotations() {
				if _, ok := merged[k]; !ok {
					merged[k] = v
				}
			}
			obj.SetAnnotations(merged)
		}
		return true, obj, tracker.Update(pa.GetResource(), obj, pa.GetNamespace())
	})

	f.client = &Client{
		Dynamic:      f.dyn,
		Typed:        f.typed,
		Mapper:       mapper,
		Log:          slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug})),
		PollInterval: 5 * time.Millisecond,
	}
	return f
}

func (f *fixture) report(phase protocol.Phase, reason, object, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, protocol.Event{Phase: phase, Reason: reason, Object: object, Message: message})
}

func (f *fixture) reasons(reason string) []protocol.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []protocol.Event
	for _, e := range f.events {
		if e.Reason == reason {
			out = append(out, e)
		}
	}
	return out
}

func (f *fixture) get(t *testing.T, gvk schema.GroupVersionKind, ns, name string) (*unstructured.Unstructured, error) {
	t.Helper()
	plural, _ := meta.UnsafeGuessKindToResource(gvk)
	obj, err := f.dyn.Tracker().Get(plural, ns, name)
	if err != nil {
		return nil, err
	}
	return obj.(*unstructured.Unstructured), nil
}

// gvkNamespace es el kind Namespace del grupo core (no de meta.k8s.io).
var gvkNamespace = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}

// namespaceObj arma un Namespace de cluster con labels.
func namespaceObj(name string, lbls map[string]string) *unstructured.Unstructured {
	ns := unstructuredObj(gvkNamespace, "", name, lbls, nil)
	ns.SetNamespace("")
	return ns
}

// unstructuredObj arma un objeto namespaced con labels y campos extra.
func unstructuredObj(gvk schema.GroupVersionKind, ns, name string, lbls map[string]string, extra map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
			"labels":    toAnyMap(lbls),
		},
	}}
	for k, v := range extra {
		u.Object[k] = v
	}
	return u
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
