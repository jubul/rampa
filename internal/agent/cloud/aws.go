// Package cloud escribe secretos de aplicación en el gestor de secretos de la
// nube con la identidad de workload del agente (IRSA, EKS Pod Identity o
// instance profile en una VM). Es el destino del comando write_secret con
// target "cloud". Hoy solo AWS Secrets Manager; los valores nunca se loguean.
package cloud

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/smithy-go"

	"rampa.dev/internal/agent/kube"
	"rampa.dev/pkg/protocol"
)

const (
	// PathPrefix es el prefijo de todos los secretos que escribe Rampa.
	PathPrefix = "rampa/"
	// TagApp y TagScope son los tags con los que se etiqueta cada secreto.
	TagApp   = "rampa:app"
	TagScope = "rampa:scope"
)

// SecretsManagerAPI es lo mínimo que se usa del cliente de Secrets Manager.
// *secretsmanager.Client lo implementa; los tests usan un mock.
type SecretsManagerAPI interface {
	CreateSecret(ctx context.Context, in *secretsmanager.CreateSecretInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error)
	PutSecretValue(ctx context.Context, in *secretsmanager.PutSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error)
}

// SecretPath es la ruta de una clave en el gestor: rampa/<app>/<scope>/<CLAVE>.
func SecretPath(app, scope, key string) string {
	return PathPrefix + app + "/" + scope + "/" + key
}

// AWSSecretsManager escribe en AWS Secrets Manager. El cliente se arma la
// primera vez que hace falta, con la cadena de credenciales por defecto del
// SDK (variables de entorno, IRSA por web identity, EKS Pod Identity, IMDS) y
// la región de AWS_REGION o, si no está, de IMDS.
type AWSSecretsManager struct {
	Log *slog.Logger

	mu     sync.Mutex
	api    SecretsManagerAPI
	newAPI func(ctx context.Context) (SecretsManagerAPI, error)
}

// NewAWSSecretsManager devuelve un escritor que arma el cliente real bajo demanda.
func NewAWSSecretsManager(log *slog.Logger) *AWSSecretsManager {
	return &AWSSecretsManager{Log: log, newAPI: newDefaultClient}
}

// NewAWSSecretsManagerWithAPI devuelve un escritor sobre un cliente ya armado (tests).
func NewAWSSecretsManagerWithAPI(api SecretsManagerAPI, log *slog.Logger) *AWSSecretsManager {
	return &AWSSecretsManager{Log: log, api: api}
}

func newDefaultClient(ctx context.Context) (SecretsManagerAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithEC2IMDSRegion())
	if err != nil {
		return nil, fmt.Errorf("no pude cargar la configuración de AWS: %w", err)
	}
	if cfg.Region == "" {
		return nil, errors.New("no pude determinar la región de AWS: seteá AWS_REGION en el agente (extraEnv del chart)")
	}
	return secretsmanager.NewFromConfig(cfg), nil
}

func (w *AWSSecretsManager) client(ctx context.Context) (SecretsManagerAPI, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.api != nil {
		return w.api, nil
	}
	if w.newAPI == nil {
		return nil, errors.New("el escritor de AWS Secrets Manager no tiene cliente")
	}
	api, err := w.newAPI(ctx)
	if err != nil {
		return nil, err
	}
	w.api = api
	return api, nil
}

// WriteSecret escribe cada clave de Values en rampa/<App>/<Scope>/<CLAVE>:
// CreateSecret con tags si no existe, PutSecretValue si ya existe. Devuelve
// las claves escritas, ordenadas. Si una clave falla se corta ahí y el error
// dice cuál y por qué; junto con el error se devuelven las claves ya escritas (la operación es
// idempotente, la API puede reintentar el comando completo).
func (w *AWSSecretsManager) WriteSecret(ctx context.Context, cmd *protocol.WriteSecretCommand) ([]string, error) {
	switch {
	case cmd == nil:
		return nil, errors.New("el comando write_secret está vacío")
	case cmd.Target != kube.TargetCloud:
		return nil, fmt.Errorf("target %q no es %q", cmd.Target, kube.TargetCloud)
	case cmd.App == "":
		return nil, errors.New("el comando write_secret no trae app")
	case cmd.Scope == "":
		return nil, errors.New("el comando write_secret no trae scope")
	case strings.ContainsAny(cmd.App+cmd.Scope, "/ "):
		return nil, errors.New("app y scope no pueden tener barras ni espacios")
	}
	keys, err := kube.SecretKeys(cmd.Values)
	if err != nil {
		return nil, err
	}
	api, err := w.client(ctx)
	if err != nil {
		return nil, describeAWSError(err, "", "")
	}
	tags := []types.Tag{
		{Key: aws.String(TagApp), Value: aws.String(cmd.App)},
		{Key: aws.String(TagScope), Value: aws.String(cmd.Scope)},
	}
	var written []string
	for _, key := range keys {
		name := SecretPath(cmd.App, cmd.Scope, key)
		value := cmd.Values[key]
		_, err := api.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
			Name:         aws.String(name),
			SecretString: aws.String(value),
			Description:  aws.String(fmt.Sprintf("Rampa: parameter %s de %s en el scope %s", key, cmd.App, cmd.Scope)),
			Tags:         tags,
		})
		if err == nil {
			w.logger().Info("secreto creado en AWS Secrets Manager", "secret", name)
			written = append(written, key)
			continue
		}
		var exists *types.ResourceExistsException
		if !errors.As(err, &exists) {
			return written, describeAWSError(err, "CreateSecret", name)
		}
		if _, err := api.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
			SecretId:     aws.String(name),
			SecretString: aws.String(value),
		}); err != nil {
			return written, describeAWSError(err, "PutSecretValue", name)
		}
		w.logger().Info("secreto actualizado en AWS Secrets Manager", "secret", name)
		written = append(written, key)
	}
	return keys, nil
}

// describeAWSError convierte los errores del SDK en mensajes accionables. Los
// errores de AWS no incluyen el valor del secreto, así que es seguro devolverlos.
func describeAWSError(err error, action, name string) error {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "AccessDeniedException", "UnauthorizedOperation", "UnrecognizedClientException":
			return fmt.Errorf("la identidad de AWS del agente no tiene permiso secretsmanager:%s sobre %s: agregá la acción a la policy del rol (IRSA, Pod Identity o instance profile) con Resource arn:aws:secretsmanager:*:*:secret:rampa/*: %s", action, name, ae.ErrorMessage())
		case "ExpiredTokenException", "InvalidClientTokenId":
			return fmt.Errorf("las credenciales de AWS del agente no son válidas (%s): revisá la identidad de workload del ServiceAccount: %s", ae.ErrorCode(), ae.ErrorMessage())
		}
		if action != "" {
			return fmt.Errorf("AWS Secrets Manager rechazó %s sobre %s: %s (%s)", action, name, ae.ErrorMessage(), ae.ErrorCode())
		}
		return fmt.Errorf("AWS rechazó la operación: %s (%s)", ae.ErrorMessage(), ae.ErrorCode())
	}
	var noRegion *aws.MissingRegionError
	if errors.As(err, &noRegion) {
		return errors.New("no pude determinar la región de AWS: seteá AWS_REGION en el agente (extraEnv del chart)")
	}
	msg := err.Error()
	if strings.Contains(msg, "failed to retrieve credentials") || strings.Contains(msg, "no EC2 IMDS role found") || strings.Contains(msg, "credentials") {
		return fmt.Errorf("el agente no tiene identidad de AWS: dale IRSA, EKS Pod Identity o un instance profile al nodo, con permisos sobre rampa/* en Secrets Manager: %w", err)
	}
	if action != "" {
		return fmt.Errorf("%s sobre %s falló: %w", action, name, err)
	}
	return err
}

func (w *AWSSecretsManager) logger() *slog.Logger {
	if w.Log == nil {
		return slog.Default()
	}
	return w.Log
}
