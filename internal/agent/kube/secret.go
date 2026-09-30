package kube

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"rampa.dev/pkg/protocol"
)

const (
	// LabelScope identifica el scope dueño de un objeto.
	LabelScope = "rampa.dev/scope"
	// LabelManagedBy es el label estándar de quién administra un objeto.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// ManagedByValue es el valor de LabelManagedBy en lo que escribe el agente.
	ManagedByValue = "rampa"
	// ParamsSecretSuffix se agrega al nombre de la app para el Secret de parameters.
	ParamsSecretSuffix = "-params"

	// MaxSecretKeys y MaxSecretValueBytes acotan un write_secret. El límite de
	// valor es el de AWS Secrets Manager (64 KiB).
	MaxSecretKeys       = 50
	MaxSecretValueBytes = 64 * 1024

	// Targets de WriteSecretCommand.
	TargetKubernetes = "kubernetes"
	TargetCloud      = "cloud"
)

// secretKeyPattern es lo que acepta Kubernetes como clave de un Secret (y lo
// que aceptan los nombres de secretos en los gestores de nube).
var secretKeyPattern = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)

// ParamsSecretName devuelve el nombre del Secret con los parameters secretos
// de una app: el mismo que el chart rampa-app usa en envFrom.
func ParamsSecretName(app string) string { return app + ParamsSecretSuffix }

// SecretKeys valida las claves y los tamaños de un WriteSecretCommand y
// devuelve las claves ordenadas. Los valores no se copian ni se incluyen en
// los errores: solo se mide su tamaño.
func SecretKeys(values map[string]string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("el comando write_secret no trae valores")
	}
	if len(values) > MaxSecretKeys {
		return nil, fmt.Errorf("el comando write_secret trae %d claves y el máximo es %d", len(values), MaxSecretKeys)
	}
	keys := make([]string, 0, len(values))
	for k, v := range values {
		if len(v) > MaxSecretValueBytes {
			return nil, fmt.Errorf("el valor de la clave %q pesa %d bytes y el máximo es %d (64 KiB)", k, len(v), MaxSecretValueBytes)
		}
		if !secretKeyPattern.MatchString(k) || len(k) > 253 {
			return nil, fmt.Errorf("la clave %q no es válida: solo letras, números, guion, punto y guion bajo", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// WriteSecret crea o actualiza el Secret <App>-params en Namespace con las
// claves de Values (merge: no borra las que no vienen). Devuelve las claves
// escritas. Guardas: el namespace no puede ser del sistema y tiene que llevar
// la label rampa.dev/app=<App> (la misma exigencia que undeploy). Si el
// namespace todavía no existe y se llama <App>-<Scope>, se crea con esas
// labels: cargar secretos antes del primer deploy es un flujo normal.
func (c *Client) WriteSecret(ctx context.Context, cmd *protocol.WriteSecretCommand) ([]string, error) {
	switch {
	case cmd == nil:
		return nil, errors.New("el comando write_secret está vacío")
	case cmd.Target != TargetKubernetes:
		return nil, fmt.Errorf("target %q no es %q", cmd.Target, TargetKubernetes)
	case cmd.App == "":
		return nil, errors.New("el comando write_secret no trae app")
	case cmd.Namespace == "":
		return nil, errors.New("el comando write_secret no trae namespace")
	case isProtectedNamespace(cmd.Namespace):
		return nil, fmt.Errorf("me niego a escribir secretos en el namespace del sistema %q", cmd.Namespace)
	case cmd.Scope == "" || cmd.Namespace != cmd.App+"-"+cmd.Scope:
		return nil, fmt.Errorf("el namespace %q no coincide con <app>-<scope> (%q-%q); no escribo secretos ahí", cmd.Namespace, cmd.App, cmd.Scope)
	}
	keys, err := SecretKeys(cmd.Values)
	if err != nil {
		return nil, err
	}
	if err := c.ensureAppNamespace(ctx, cmd); err != nil {
		return nil, err
	}

	name := ParamsSecretName(cmd.App)
	lbls := map[string]string{
		LabelApp:       cmd.App,
		LabelScope:     cmd.Scope,
		LabelManagedBy: ManagedByValue,
	}
	secrets := c.Typed.CoreV1().Secrets(cmd.Namespace)
	ref := fmt.Sprintf("Secret %s/%s", cmd.Namespace, name)

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := secrets.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			s := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cmd.Namespace, Labels: lbls},
				Type:       corev1.SecretTypeOpaque,
				Data:       make(map[string][]byte, len(cmd.Values)),
			}
			for k, v := range cmd.Values {
				s.Data[k] = []byte(v)
			}
			_, err = secrets.Create(ctx, s, metav1.CreateOptions{FieldManager: FieldManager})
			if apierrors.IsAlreadyExists(err) {
				// Carrera con otro escritor: reintentamos por la rama de update.
				return apierrors.NewConflict(corev1.Resource("secrets"), name, err)
			}
			if err != nil {
				return fmt.Errorf("no pude crear el %s: %w", ref, err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("no pude consultar el %s: %w", ref, err)
		}
		if managedByESO(existing) {
			return fmt.Errorf("el %s lo administra External Secrets Operator: este scope usa un gestor de secretos de nube, cargá los secretos con target cloud (ESO revertiría cualquier cambio directo)", ref)
		}
		if existing.Type != "" && existing.Type != corev1.SecretTypeOpaque {
			return fmt.Errorf("el %s existe con tipo %q y no Opaque; no lo toco", ref, existing.Type)
		}
		if owner, ok := existing.Labels[LabelManagedBy]; ok && owner != ManagedByValue {
			return fmt.Errorf("el %s lo administra %q (label %s); no lo toco", ref, owner, LabelManagedBy)
		}
		if existing.Data == nil {
			existing.Data = make(map[string][]byte, len(cmd.Values))
		}
		for k, v := range cmd.Values {
			existing.Data[k] = []byte(v)
		}
		if existing.Labels == nil {
			existing.Labels = map[string]string{}
		}
		for k, v := range lbls {
			existing.Labels[k] = v
		}
		if _, err := secrets.Update(ctx, existing, metav1.UpdateOptions{FieldManager: FieldManager}); err != nil {
			if apierrors.IsConflict(err) {
				return err
			}
			return fmt.Errorf("no pude actualizar el %s: %w", ref, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	c.logger().Info("secretos de aplicación escritos", "namespace", cmd.Namespace, "secret", name, "keys", keys)
	return keys, nil
}

// ensureAppNamespace verifica que el namespace pertenezca a la app. Si no
// existe y sigue la convención <app>-<scope>, lo crea con las labels de la app
// para que el primer deploy lo encuentre listo.
func (c *Client) ensureAppNamespace(ctx context.Context, cmd *protocol.WriteSecretCommand) error {
	ns, err := c.Typed.CoreV1().Namespaces().Get(ctx, cmd.Namespace, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		if cmd.Scope == "" || cmd.Namespace != cmd.App+"-"+cmd.Scope {
			return fmt.Errorf("el namespace %q no existe y no coincide con <app>-<scope> (%q-%q); no lo creo", cmd.Namespace, cmd.App, cmd.Scope)
		}
		_, err := c.Typed.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   cmd.Namespace,
				Labels: map[string]string{LabelApp: cmd.App, LabelScope: cmd.Scope, LabelManagedBy: ManagedByValue},
			},
		}, metav1.CreateOptions{FieldManager: FieldManager})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("no pude crear el namespace %s: %w", cmd.Namespace, err)
		}
		if err == nil {
			c.logger().Info("namespace creado para recibir secretos", "namespace", cmd.Namespace, "app", cmd.App)
			return nil
		}
		// Apareció entre el Get y el Create: verificamos la label igual.
		ns, err = c.Typed.CoreV1().Namespaces().Get(ctx, cmd.Namespace, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("no pude consultar el namespace %s: %w", cmd.Namespace, err)
		}
	case err != nil:
		return fmt.Errorf("no pude consultar el namespace %s: %w", cmd.Namespace, err)
	}
	if ns.DeletionTimestamp != nil {
		return fmt.Errorf("el namespace %s se está borrando; reintentá cuando termine", cmd.Namespace)
	}
	return checkNamespaceOwner(cmd.Namespace, cmd.App, ns.Labels)
}

// managedByESO dice si el Secret lo genera External Secrets Operator.
func managedByESO(s *corev1.Secret) bool {
	if _, ok := s.Labels["reconcile.external-secrets.io/managed"]; ok {
		return true
	}
	for _, o := range s.OwnerReferences {
		if strings.HasPrefix(o.APIVersion, "external-secrets.io/") {
			return true
		}
	}
	return false
}
