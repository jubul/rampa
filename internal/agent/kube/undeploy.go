package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"rampa.dev/pkg/protocol"
)

// Namespaces que el agente nunca borra ni usa como destino de un deploy, pase
// lo que pase: los del sistema y el propio del agente.
var protectedNamespaces = map[string]bool{
	"default":         true,
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,
	"rampa-system":    true,
}

// isProtectedNamespace dice si el namespace es del sistema (kube-*, default,
// rampa-system).
func isProtectedNamespace(name string) bool {
	return protectedNamespaces[name] || strings.HasPrefix(name, "kube-")
}

// Undeploy borra el namespace del scope. Con Wait espera hasta UndeployWait a
// que desaparezca. Como es destructivo, exige que el namespace siga la
// convención <app>-<scope>, que no sea un namespace del sistema y que lleve la
// label rampa.dev/app=<app>: solo se borra lo que el agente mismo creó para
// esa app.
func (c *Client) Undeploy(ctx context.Context, cmd *protocol.UndeployCommand, report Reporter) error {
	if report == nil {
		report = func(protocol.Phase, string, string, string) {}
	}
	if cmd == nil || cmd.Namespace == "" {
		return errors.New("el comando undeploy no trae namespace")
	}
	if isProtectedNamespace(cmd.Namespace) {
		return fmt.Errorf("me niego a borrar el namespace del sistema %q", cmd.Namespace)
	}
	if cmd.App == "" || cmd.Scope == "" || cmd.Namespace != cmd.App+"-"+cmd.Scope {
		return fmt.Errorf("el namespace %q no coincide con <app>-<scope> (%q-%q); no lo borro", cmd.Namespace, cmd.App, cmd.Scope)
	}

	nsClient := c.Dynamic.Resource(gvrNamespaces)
	ref := "Namespace/" + cmd.Namespace
	existing, err := nsClient.Get(ctx, cmd.Namespace, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		report(protocol.PhaseHealthy, "Deleted", ref, "el namespace ya no existía")
		return nil
	case err != nil:
		return fmt.Errorf("no pude consultar el namespace %s: %w", cmd.Namespace, err)
	}
	if owner := existing.GetLabels()[LabelApp]; owner != cmd.App {
		return fmt.Errorf("el namespace %q no tiene la label %s=%q (tiene %q); no lo borro", cmd.Namespace, LabelApp, cmd.App, owner)
	}

	policy := metav1.DeletePropagationBackground
	err = nsClient.Delete(ctx, cmd.Namespace, metav1.DeleteOptions{PropagationPolicy: &policy})
	switch {
	case apierrors.IsNotFound(err):
		report(protocol.PhaseHealthy, "Deleted", ref, "el namespace ya no existía")
		return nil
	case err != nil:
		return fmt.Errorf("no pude borrar el namespace %s: %w", cmd.Namespace, err)
	}
	report(protocol.PhaseApplying, "Deleting", ref, "borrado pedido")
	c.logger().Info("namespace borrado", "namespace", cmd.Namespace)

	if !cmd.Wait {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, UndeployWait)
	defer cancel()
	ticker := time.NewTicker(c.pollInterval())
	defer ticker.Stop()
	for {
		_, err := nsClient.Get(waitCtx, cmd.Namespace, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			report(protocol.PhaseHealthy, "Deleted", ref, "el namespace desapareció")
			return nil
		}
		if err != nil && waitCtx.Err() == nil {
			return fmt.Errorf("no pude consultar el namespace mientras esperaba: %w", err)
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("%w: el namespace %s sigue existiendo después de %s", ErrTimeout, cmd.Namespace, UndeployWait)
		case <-ticker.C:
		}
	}
}
