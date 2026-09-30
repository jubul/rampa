package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"rampa.dev/internal/agent/kube"
	"rampa.dev/pkg/protocol"
)

type fakeKube struct {
	deployStatus *protocol.WorkloadStatus
	deployErr    error
	undeployErr  error
	status       *protocol.WorkloadStatus
	statusErr    error
	logLines     []string
	logsErr      error
	lastDeploy   *protocol.DeployCommand
	writeKeys    []string
	writeErr     error
	lastWrite    *protocol.WriteSecretCommand
}

func (f *fakeKube) Deploy(_ context.Context, cmd *protocol.DeployCommand, report kube.Reporter) (*protocol.WorkloadStatus, error) {
	f.lastDeploy = cmd
	report(protocol.PhaseApplying, "Applied", "Service/x", "aplicado")
	return f.deployStatus, f.deployErr
}
func (f *fakeKube) Undeploy(_ context.Context, _ *protocol.UndeployCommand, report kube.Reporter) error {
	report(protocol.PhaseApplying, "Deleting", "Namespace/x", "borrando")
	return f.undeployErr
}
func (f *fakeKube) Status(context.Context, *protocol.StatusCommand) (*protocol.WorkloadStatus, error) {
	return f.status, f.statusErr
}
func (f *fakeKube) Logs(_ context.Context, _ *protocol.LogsCommand, sink kube.LogSink) error {
	if f.logsErr != nil {
		return f.logsErr
	}
	if err := sink(protocol.LogChunk{Pod: "p", Lines: f.logLines}); err != nil {
		return err
	}
	return sink(protocol.LogChunk{EOF: true})
}

func (f *fakeKube) WriteSecret(_ context.Context, cmd *protocol.WriteSecretCommand) ([]string, error) {
	f.lastWrite = cmd
	return f.writeKeys, f.writeErr
}

// fakeCloud es el gestor de secretos de nube de mentira.
type fakeCloud struct {
	keys []string
	err  error
	last *protocol.WriteSecretCommand
}

func (f *fakeCloud) WriteSecret(_ context.Context, cmd *protocol.WriteSecretCommand) ([]string, error) {
	f.last = cmd
	return f.keys, f.err
}

type memSink struct {
	events []protocol.Event
	logs   []protocol.LogChunk
}

func (s *memSink) Event(ev protocol.Event) error {
	s.events = append(s.events, ev)
	return nil
}

func (s *memSink) Log(chunk protocol.LogChunk) error {
	s.logs = append(s.logs, chunk)
	return nil
}

func (s *memSink) reset() { s.events, s.logs = nil, nil }

func deploy(id string, timeout int) protocol.Command {
	return protocol.Command{ID: id, Kind: protocol.CommandDeploy, Deploy: &protocol.DeployCommand{App: "a", Namespace: "a-dev", DeploymentID: "d", TimeoutSeconds: timeout}}
}

func undeploy(id string) protocol.Command {
	return protocol.Command{ID: id, Kind: protocol.CommandUndeploy, Undeploy: &protocol.UndeployCommand{App: "a", Scope: "dev", Namespace: "a-dev"}}
}

func healthy() *protocol.WorkloadStatus {
	return &protocol.WorkloadStatus{Kind: "Deployment", Phase: protocol.PhaseHealthy, Ready: 2, Desired: 2}
}

func must(t *testing.T, cond bool, msg string, a ...any) {
	t.Helper()
	if !cond {
		t.Errorf(msg, a...)
	}
}

func TestDeployOutcomes(t *testing.T) {
	sink := &memSink{}
	k := &fakeKube{deployStatus: healthy()}
	e := &Executor{Kube: k, DefaultDeployTimeout: 7 * time.Minute}

	res := e.Execute(context.Background(), deploy("c1", 0), sink)
	must(t, res.Status == protocol.ResultSucceeded && res.CommandID == "c1", "succeeded: %+v", res)
	must(t, res.Workload != nil && res.Workload.Ready == 2, "workload: %+v", res.Workload)
	must(t, k.lastDeploy.TimeoutSeconds == 420, "timeout default: %d", k.lastDeploy.TimeoutSeconds)
	must(t, len(sink.events) == 1 && sink.events[0].Reason == "Applied" && !sink.events[0].Time.IsZero(), "eventos: %+v", sink.events)

	sink.reset()
	k.deployErr = fmt.Errorf("envuelto: %w", kube.ErrTimeout)
	k.deployStatus = &protocol.WorkloadStatus{Kind: "Deployment", Phase: protocol.PhaseProgressing, Ready: 1, Desired: 2}
	res = e.Execute(context.Background(), deploy("c2", 30), sink)
	must(t, res.Status == protocol.ResultTimeout, "timeout: %+v", res)
	must(t, res.Workload != nil && res.Workload.Ready == 1, "workload en timeout: %+v", res.Workload)
	must(t, k.lastDeploy.TimeoutSeconds == 30, "timeout del comando se respeta: %d", k.lastDeploy.TimeoutSeconds)

	k.deployErr = &kube.UnhealthyError{Reason: "CrashLoopBackOff", Message: "pod roto"}
	k.deployStatus = &protocol.WorkloadStatus{Kind: "Deployment", Phase: protocol.PhaseFailed}
	res = e.Execute(context.Background(), deploy("c3", 30), sink)
	must(t, res.Status == protocol.ResultFailed && res.Message == "CrashLoopBackOff: pod roto", "failed: %+v", res)

	k.deployErr = &kube.MissingKindError{}
	k.deployStatus = nil
	res = e.Execute(context.Background(), deploy("c4", 30), sink)
	must(t, res.Status == protocol.ResultFailed && res.Workload == nil, "crd faltante: %+v", res)

	res = e.Execute(context.Background(), protocol.Command{ID: "c5", Kind: protocol.CommandDeploy}, sink)
	must(t, res.Status == protocol.ResultRejected, "sin payload: %+v", res)
}

func TestOtherCommands(t *testing.T) {
	sink := &memSink{}
	k := &fakeKube{status: healthy(), logLines: []string{"l1", "l2"}}
	e := &Executor{Kube: k}

	res := e.Execute(context.Background(), undeploy("u1"), sink)
	must(t, res.Status == protocol.ResultSucceeded && len(sink.events) == 1, "undeploy ok: %+v %+v", res, sink.events)
	k.undeployErr = kube.ErrTimeout
	res = e.Execute(context.Background(), undeploy("u2"), sink)
	must(t, res.Status == protocol.ResultTimeout, "undeploy timeout: %+v", res)
	k.undeployErr = errors.New("me niego")
	res = e.Execute(context.Background(), undeploy("u3"), sink)
	must(t, res.Status == protocol.ResultFailed && res.Message == "me niego", "undeploy failed: %+v", res)

	res = e.Execute(context.Background(), protocol.Command{ID: "s1", Kind: protocol.CommandStatus, Status: &protocol.StatusCommand{App: "a", Namespace: "a-dev"}}, sink)
	must(t, res.Status == protocol.ResultSucceeded && res.Workload == k.status && res.Message == "healthy", "status: %+v", res)
	k.statusErr = errors.New("no encontré")
	res = e.Execute(context.Background(), protocol.Command{ID: "s2", Kind: protocol.CommandStatus, Status: &protocol.StatusCommand{App: "a", Namespace: "a-dev"}}, sink)
	must(t, res.Status == protocol.ResultFailed, "status failed: %+v", res)

	sink.reset()
	res = e.Execute(context.Background(), protocol.Command{ID: "l1", Kind: protocol.CommandLogs, Logs: &protocol.LogsCommand{App: "a", Namespace: "a-dev"}}, sink)
	must(t, res.Status == protocol.ResultSucceeded, "logs: %+v", res)
	must(t, len(sink.logs) == 2 && sink.logs[0].CommandID == "l1" && sink.logs[1].EOF, "chunks: %+v", sink.logs)
	k.logsErr = errors.New("sin pods")
	res = e.Execute(context.Background(), protocol.Command{ID: "l2", Kind: protocol.CommandLogs, Logs: &protocol.LogsCommand{App: "a", Namespace: "a-dev"}}, sink)
	must(t, res.Status == protocol.ResultFailed, "logs failed: %+v", res)

	res = e.Execute(context.Background(), protocol.Command{ID: "p1", Kind: "provision"}, sink)
	must(t, res.Status == protocol.ResultRejected && res.CommandID == "p1", "rejected: %+v", res)
	for _, c := range []protocol.Command{{ID: "x", Kind: protocol.CommandUndeploy}, {ID: "x", Kind: protocol.CommandStatus}, {ID: "x", Kind: protocol.CommandLogs}} {
		must(t, e.Execute(context.Background(), c, sink).Status == protocol.ResultRejected, "%s sin payload debería ser rejected", c.Kind)
	}
}

func writeSecret(id, target string, values map[string]string) protocol.Command {
	return protocol.Command{ID: id, Kind: protocol.CommandWriteSecret, WriteSecret: &protocol.WriteSecretCommand{
		App: "checkout", Scope: "dev", Namespace: "checkout-dev", Target: target, Values: values,
	}}
}

func TestWriteSecretOutcomes(t *testing.T) {
	sink := &memSink{}
	k := &fakeKube{writeKeys: []string{"API_KEY", "DB_URL"}}
	cloud := &fakeCloud{keys: []string{"API_KEY"}}
	e := &Executor{Kube: k, Cloud: cloud}
	values := map[string]string{"DB_URL": "postgres://secreto", "API_KEY": "valor-secreto"}

	res := e.Execute(context.Background(), writeSecret("w1", "kubernetes", values), sink)
	must(t, res.Status == protocol.ResultSucceeded && res.CommandID == "w1", "kubernetes ok: %+v", res)
	must(t, k.lastWrite != nil && k.lastWrite.Target == "kubernetes" && cloud.last == nil, "fue al kube: %+v %+v", k.lastWrite, cloud.last)
	must(t, strings.Contains(res.Message, "Secret checkout-dev/checkout-params") && strings.Contains(res.Message, "API_KEY, DB_URL"), "mensaje: %s", res.Message)
	must(t, len(sink.events) == 1 && sink.events[0].Reason == "SecretWritten" && strings.Contains(sink.events[0].Message, "API_KEY, DB_URL"), "evento: %+v", sink.events)
	// Ni el result ni los eventos llevan valores.
	for _, v := range values {
		must(t, !strings.Contains(res.Message, v), "el result contiene un valor: %s", res.Message)
		for _, ev := range sink.events {
			must(t, !strings.Contains(ev.Message, v), "el evento contiene un valor: %s", ev.Message)
		}
	}

	sink.reset()
	res = e.Execute(context.Background(), writeSecret("w2", "cloud", values), sink)
	must(t, res.Status == protocol.ResultSucceeded && cloud.last != nil && cloud.last.Target == "cloud", "cloud ok: %+v", res)
	must(t, strings.Contains(res.Message, "rampa/checkout/dev/*") && strings.Contains(res.Message, "API_KEY"), "mensaje cloud: %s", res.Message)
	must(t, len(sink.events) == 1 && sink.events[0].Reason == "SecretWritten", "evento cloud: %+v", sink.events)

	k.writeErr = errors.New("el namespace no tiene la label")
	res = e.Execute(context.Background(), writeSecret("w3", "kubernetes", values), sink)
	must(t, res.Status == protocol.ResultFailed && res.Message == "el namespace no tiene la label", "kubernetes failed: %+v", res)

	cloud.err = errors.New("sin permiso")
	cloud.keys = nil
	res = e.Execute(context.Background(), writeSecret("w4", "cloud", values), sink)
	must(t, res.Status == protocol.ResultFailed && res.Message == "sin permiso", "cloud failed: %+v", res)

	cloud.keys = []string{"A", "B"}
	res = e.Execute(context.Background(), writeSecret("w4b", "cloud", values), sink)
	must(t, res.Status == protocol.ResultFailed && strings.Contains(res.Message, "sin permiso") && strings.Contains(res.Message, "claves escritas antes del fallo: A, B"), "cloud parcial: %+v", res)

	e.Cloud = nil
	res = e.Execute(context.Background(), writeSecret("w5", "cloud", values), sink)
	must(t, res.Status == protocol.ResultFailed && strings.Contains(res.Message, "no tiene configurado un gestor de secretos"), "sin cloud: %+v", res)

	res = e.Execute(context.Background(), writeSecret("w6", "vault", values), sink)
	must(t, res.Status == protocol.ResultRejected && strings.Contains(res.Message, `target "vault"`), "target desconocido: %+v", res)
	res = e.Execute(context.Background(), writeSecret("w7", "kubernetes", nil), sink)
	must(t, res.Status == protocol.ResultRejected, "sin valores: %+v", res)
	res = e.Execute(context.Background(), protocol.Command{ID: "w8", Kind: protocol.CommandWriteSecret}, sink)
	must(t, res.Status == protocol.ResultRejected, "sin payload: %+v", res)
}
