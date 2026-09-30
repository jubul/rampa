// Package aws implementa provider.Provider para Amazon Web Services.
//
// Conexión de cuenta: el cliente crea un rol IAM con una plantilla de
// CloudFormation (quick-create) que confía en la identidad del plano de control
// de Rampa y exige un external ID único por cuenta. Rampa asume ese rol con STS
// y nunca guarda access keys.
//
// Cluster: EKS. Registry: ECR. Secretos: Secrets Manager vía External Secrets
// con IRSA. Identidad de pods: IRSA (anotación eks.amazonaws.com/role-arn).
package aws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"rampa.dev/pkg/provider"
)

const (
	// EnvTrustedPrincipalARN es la identidad IAM del plano de control (el rol o
	// usuario con el que corre la API) que va a asumir los roles de los clientes.
	EnvTrustedPrincipalARN = "RAMPA_AWS_TRUSTED_PRINCIPAL_ARN"
	// EnvDefaultRegion es la región que se usa en el link de CloudFormation y
	// como región del cliente STS base cuando el entorno no define ninguna.
	EnvDefaultRegion = "RAMPA_AWS_DEFAULT_REGION"
	// EnvTemplateURL es la URL en S3 (https://<bucket>.s3.amazonaws.com/... o
	// CloudFront delante de S3) donde está publicada rampa-connect.yaml. La
	// consola de CloudFormation solo acepta plantillas alojadas en S3, así que
	// sin esto no hay link de quick-create y las instrucciones son solo por CLI.
	EnvTemplateURL = "RAMPA_AWS_TEMPLATE_URL"

	// DefaultRegion se usa cuando ni la cuenta ni el entorno traen región.
	DefaultRegion = "us-east-1"
	// DefaultSecretStoreName es el nombre del ClusterSecretStore que instala el agente.
	DefaultSecretStoreName = "aws-secrets-manager"
	// StackName es el nombre sugerido del stack de CloudFormation.
	StackName = "RampaConnect"
	// TemplatePath es la ruta pública donde la API sirve CloudFormationTemplate.
	TemplatePath = "/public/cloudformation/rampa-connect.yaml"
	// SettingExternalID es la clave de CloudAccount.Settings con el external ID.
	SettingExternalID = "external_id"

	// agentServiceAccount y agentNamespace son la identidad del agente en el
	// cluster, la que External Secrets usa (IRSA) para leer Secrets Manager.
	agentServiceAccount = "rampa-agent"
	agentNamespace      = "rampa-system"

	// DefaultModuleSource es la base de los módulos OpenTofu de services.
	DefaultModuleSource = "git::https://github.com/CHANGEME/rampa.git"

	verifyTimeout = 15 * time.Second
)

// STSClient es lo mínimo que necesitamos de STS. *sts.Client lo cumple y los
// tests lo mockean.
type STSClient interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Config configura el provider.
type Config struct {
	// TrustedPrincipalARN es la identidad IAM del plano de control. Va a la
	// trust policy del rol del cliente. Sin esto Verify falla con un error claro.
	TrustedPrincipalARN string
	// DefaultRegion se usa en el link de CloudFormation y como fallback.
	DefaultRegion string
	// TemplateURL es la URL en S3 de la plantilla rampa-connect.yaml para el
	// link de quick-create. Vacío: no hay link y el comando descarga la
	// plantilla de la API y la pasa con --template-body.
	TemplateURL string
	// STSClient es el cliente con la identidad del plano de control, el que
	// llama AssumeRole. Opcional: si es nil se carga la config default de AWS
	// la primera vez que hace falta.
	STSClient STSClient
	// NewAssumedClient construye el cliente STS que corre con las credenciales
	// del rol asumido, para GetCallerIdentity. Opcional; los tests lo mockean.
	NewAssumedClient func(creds awssdk.CredentialsProvider, region string) STSClient
	// ModuleSource es la base de los módulos OpenTofu (git::https://...).
	ModuleSource string
}

// Provider es la implementación de AWS.
type Provider struct {
	cfg Config

	once    sync.Once
	baseSTS STSClient
	baseErr error
}

var _ provider.Provider = (*Provider)(nil)

// serviceModules son los tipos de service y si su módulo ya existe en terraform/modules/aws.
var serviceModules = map[string]bool{
	"postgres": true,
	"redis":    false,
	"sqs":      false,
	"s3":       false,
	"dynamodb": false,
}

// New crea el provider con una configuración explícita.
func New(cfg Config) *Provider {
	if cfg.DefaultRegion == "" {
		cfg.DefaultRegion = DefaultRegion
	}
	if cfg.ModuleSource == "" {
		cfg.ModuleSource = DefaultModuleSource
	}
	if cfg.NewAssumedClient == nil {
		cfg.NewAssumedClient = func(creds awssdk.CredentialsProvider, region string) STSClient {
			return sts.New(sts.Options{Region: region, Credentials: awssdk.NewCredentialsCache(creds)})
		}
	}
	p := &Provider{cfg: cfg}
	if cfg.STSClient != nil {
		p.once.Do(func() { p.baseSTS = cfg.STSClient })
	}
	return p
}

// NewFromEnv lee RAMPA_AWS_TRUSTED_PRINCIPAL_ARN, RAMPA_AWS_DEFAULT_REGION y
// RAMPA_AWS_TEMPLATE_URL. Si la primera falta, el provider igual queda
// disponible pero Verify devuelve un error que dice qué configurar. Sin la
// tercera, las instrucciones no traen link de quick-create.
func NewFromEnv() *Provider {
	cfg := Config{
		TrustedPrincipalARN: strings.TrimSpace(os.Getenv(EnvTrustedPrincipalARN)),
		DefaultRegion:       strings.TrimSpace(os.Getenv(EnvDefaultRegion)),
		TemplateURL:         strings.TrimSpace(os.Getenv(EnvTemplateURL)),
	}
	if cfg.TrustedPrincipalARN == "" {
		slog.Warn("provider aws: falta la identidad del plano de control; Verify va a fallar hasta que se configure", "env", EnvTrustedPrincipalARN)
	}
	if cfg.TemplateURL == "" {
		slog.Warn("provider aws: sin plantilla en S3, las instrucciones de conexión no tienen link de quick-create, solo el comando de CLI", "env", EnvTemplateURL)
	}
	return New(cfg)
}

func init() {
	provider.Register(NewFromEnv())
}

// Kind implementa provider.Provider.
func (p *Provider) Kind() provider.Kind { return provider.AWS }

// DisplayName implementa provider.Provider.
func (p *Provider) DisplayName() string { return "Amazon Web Services" }

// Available implementa provider.Provider. AWS está completo en la Fase A.
func (p *Provider) Available() bool { return true }

// Auth implementa provider.Provider: rol IAM asumido por federación, sin secretos.
func (p *Provider) Auth() provider.AuthKind { return provider.AuthFederated }

// Terminology implementa provider.Provider con los nombres de AWS.
func (p *Provider) Terminology() provider.Terminology {
	return provider.Terminology{
		Account:     provider.LocalizedTerm{ES: "cuenta de AWS", EN: "AWS account"},
		Project:     provider.LocalizedTerm{ES: "cuenta de AWS", EN: "AWS account"},
		Cluster:     provider.LocalizedTerm{ES: "cluster EKS", EN: "EKS cluster"},
		VM:          provider.LocalizedTerm{ES: "instancia EC2", EN: "EC2 instance"},
		Registry:    provider.LocalizedTerm{ES: "ECR", EN: "ECR"},
		SecretStore: provider.LocalizedTerm{ES: "Secrets Manager", EN: "Secrets Manager"},
		Region:      provider.LocalizedTerm{ES: "región", EN: "region"},
	}
}

// ConnectInstructions arma las instrucciones para crear el rol con
// CloudFormation. La consola solo acepta plantillas alojadas en S3: si
// Config.TemplateURL está seteada, hay link de quick-create y el comando usa
// --template-url; si no, URL queda vacío y el comando descarga la plantilla
// que sirve la API (callbackURL + TemplatePath) y la pasa con --template-body.
func (p *Provider) ConnectInstructions(req provider.ConnectRequest) provider.Instructions {
	orgSlug, externalID, callbackURL := req.OrgSlug, req.ExternalID, req.CallbackURL
	apiTemplateURL := strings.TrimRight(callbackURL, "/") + TemplatePath
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	params := "--parameters ParameterKey=ExternalId,ParameterValue=" + shellQuote(externalID) +
		" ParameterKey=TrustedPrincipalArn,ParameterValue=" + shellQuote(p.cfg.TrustedPrincipalARN)

	var quickCreate, command, howTo string
	if p.cfg.TemplateURL != "" {
		q := url.Values{}
		q.Set("templateURL", p.cfg.TemplateURL)
		q.Set("stackName", StackName)
		q.Set("param_ExternalId", externalID)
		q.Set("param_TrustedPrincipalArn", p.cfg.TrustedPrincipalARN)
		quickCreate = fmt.Sprintf("https://console.aws.amazon.com/cloudformation/home?region=%s#/stacks/quickcreate?%s",
			url.QueryEscape(region), q.Encode())
		command = strings.Join([]string{
			"aws cloudformation create-stack",
			"--stack-name " + StackName,
			"--template-url " + shellQuote(p.cfg.TemplateURL),
			"--capabilities CAPABILITY_NAMED_IAM",
			params,
		}, " \\\n  ")
		howTo = "El botón abre CloudFormation en tu consola con la plantilla ya cargada. " +
			"Si preferís la CLI, el comando hace exactamente lo mismo."
	} else {
		command = "curl -fsSL -o rampa-connect.yaml " + shellQuote(apiTemplateURL) + " && \\\n" + strings.Join([]string{
			"aws cloudformation create-stack",
			"--stack-name " + StackName,
			"--region " + shellQuote(region),
			"--template-body file://rampa-connect.yaml",
			"--capabilities CAPABILITY_NAMED_IAM",
			params,
		}, " \\\n  ")
		howTo = "Esta instalación de Rampa no publica la plantilla en S3, así que no hay botón de un click: " +
			"el comando descarga la plantilla desde Rampa (" + apiTemplateURL + ") y crea el stack con la CLI de AWS. " +
			"También podés bajarla y subirla a mano en la consola de CloudFormation (Create stack, Upload a template file)."
	}

	description := fmt.Sprintf(
		"Rampa opera en tu cuenta de AWS asumiendo un rol IAM que vos creás; nunca te pedimos access keys. "+
			"El stack crea el rol RampaConnect-%s con permisos de mínimo privilegio "+
			"(ECR sobre repos rampa/*, EKS solo lectura, Secrets Manager sobre secretos rampa/*) y una trust policy que solo permite asumirlo "+
			"a la identidad de Rampa con el external ID de la organización %s. %s "+
			"Cuando el stack termine (CREATE_COMPLETE), copiá el output RoleArn y pegalo abajo.",
		StackName, orgSlug, howTo)

	return provider.Instructions{
		Title:       "Conectar una cuenta de AWS",
		Description: description,
		URL:         quickCreate,
		Command:     command,
		Fields: []provider.Field{{
			Name:        "external_ref",
			Label:       "ARN del rol creado",
			Placeholder: "arn:aws:iam::123456789012:role/RampaConnect-" + StackName,
			Help:        "Está en la pestaña Outputs del stack, como RoleArn.",
			Required:    true,
		}},
	}
}

// Verify asume el rol de la cuenta con el external ID y pregunta quién somos.
//
// Devuelve error cuando ni siquiera pudimos intentarlo (falta configuración
// nuestra o datos de la cuenta). Cuando el intento se hace y AWS lo rechaza,
// devuelve Verification con OK false y el motivo en Message, sin error.
func (p *Provider) Verify(ctx context.Context, acc provider.CloudAccount) (provider.Verification, error) {
	if p.cfg.TrustedPrincipalARN == "" {
		return provider.Verification{}, fmt.Errorf("provider aws: falta la identidad del plano de control: configurá %s con el ARN del rol o usuario IAM con el que corre la API", EnvTrustedPrincipalARN)
	}
	arn, err := parseRoleARN(acc.ExternalRef)
	if err != nil {
		return provider.Verification{}, fmt.Errorf("provider aws: cuenta %q: %w", acc.Name, err)
	}
	externalID := strings.TrimSpace(acc.Settings[SettingExternalID])
	if externalID == "" {
		return provider.Verification{}, fmt.Errorf("provider aws: cuenta %q: falta settings.%s; la API lo genera al crear la cuenta", acc.Name, SettingExternalID)
	}
	region := acc.Region
	if region == "" {
		region = p.cfg.DefaultRegion
	}

	base, err := p.baseClient(ctx, region)
	if err != nil {
		return provider.Verification{}, fmt.Errorf("provider aws: no pude armar el cliente STS del plano de control: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	assume := stscreds.NewAssumeRoleProvider(base, acc.ExternalRef, func(o *stscreds.AssumeRoleOptions) {
		o.ExternalID = awssdk.String(externalID)
		o.RoleSessionName = sessionName(acc)
		o.Duration = 15 * time.Minute
	})
	creds, err := assume.Retrieve(ctx)
	if err != nil {
		slog.Info("provider aws: AssumeRole rechazado", "account", acc.Name, "role_account", arn.AccountID, "err", err)
		return provider.Verification{
			OK:      false,
			Message: fmt.Sprintf("no pude asumir el rol %s: %s. Revisá que el stack de CloudFormation esté en CREATE_COMPLETE, que el ARN sea el del output RoleArn y que el external ID no se haya cambiado.", acc.ExternalRef, describeErr(err)),
		}, nil
	}

	assumed := p.cfg.NewAssumedClient(staticCredentials{creds}, region)
	out, err := assumed.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		slog.Info("provider aws: GetCallerIdentity con el rol asumido falló", "account", acc.Name, "err", err)
		return provider.Verification{
			OK:      false,
			Message: fmt.Sprintf("asumí el rol pero GetCallerIdentity falló: %s", describeErr(err)),
		}, nil
	}
	identity := awssdk.ToString(out.Arn)
	if identity == "" {
		return provider.Verification{OK: false, Message: "STS devolvió una identidad vacía"}, nil
	}
	slog.Info("provider aws: cuenta verificada", "account", acc.Name, "identity", identity)
	return provider.Verification{
		OK:       true,
		Identity: identity,
		Message:  fmt.Sprintf("rol asumido correctamente en la cuenta %s", awssdk.ToString(out.Account)),
	}, nil
}

// Registry devuelve el host de ECR de la cuenta: <cuenta>.dkr.ecr.<región>.amazonaws.com.
func (p *Provider) Registry(acc provider.CloudAccount) (string, error) {
	arn, err := parseRoleARN(acc.ExternalRef)
	if err != nil {
		return "", fmt.Errorf("provider aws: no puedo derivar el registry de la cuenta %q: %w", acc.Name, err)
	}
	region := strings.TrimSpace(acc.Region)
	if region == "" {
		return "", fmt.Errorf("provider aws: la cuenta %q no tiene región; ECR es regional", acc.Name)
	}
	return fmt.Sprintf("%s.dkr.ecr.%s.%s", arn.AccountID, region, dnsSuffix(arn.Partition)), nil
}

// SecretStoreSpec arma el ClusterSecretStore de External Secrets hacia Secrets
// Manager, autenticado con la ServiceAccount del agente vía IRSA.
func (p *Provider) SecretStoreSpec(acc provider.CloudAccount, cluster provider.ClusterInfo) provider.SecretStoreSpec {
	region := cluster.Region
	if region == "" {
		region = acc.Region
	}
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	name := DefaultSecretStoreName
	return provider.SecretStoreSpec{
		Name: name,
		Object: map[string]any{
			"apiVersion": "external-secrets.io/v1",
			"kind":       "ClusterSecretStore",
			"metadata": map[string]any{
				"name": name,
				"labels": map[string]any{
					"app.kubernetes.io/managed-by": "rampa",
					"rampa.dev/provider":           string(provider.AWS),
				},
			},
			"spec": map[string]any{
				"provider": map[string]any{
					"aws": map[string]any{
						"service": "SecretsManager",
						"region":  region,
						"auth": map[string]any{
							"jwt": map[string]any{
								"serviceAccountRef": map[string]any{
									"name":      agentServiceAccount,
									"namespace": agentNamespace,
								},
							},
						},
					},
				},
			},
		},
	}
}

// WorkloadIdentityAnnotations devuelve la anotación de IRSA para el ServiceAccount de la app.
func (p *Provider) WorkloadIdentityAnnotations(_ provider.CloudAccount, identity string) map[string]string {
	if strings.TrimSpace(identity) == "" {
		return map[string]string{}
	}
	return map[string]string{"eks.amazonaws.com/role-arn": identity}
}

// ServiceModule apunta a terraform/modules/aws/<kind>. Solo postgres existe hoy.
func (p *Provider) ServiceModule(kind string) (provider.ModuleRef, bool) {
	exists, known := serviceModules[kind]
	ref := provider.ModuleRef{Source: fmt.Sprintf("%s//terraform/modules/aws/%s?ref=main", p.cfg.ModuleSource, kind)}
	if !known || !exists {
		return ref, false
	}
	return ref, true
}

// baseClient devuelve el cliente STS con la identidad del plano de control,
// cargándolo de la config default de AWS la primera vez.
func (p *Provider) baseClient(ctx context.Context, region string) (STSClient, error) {
	p.once.Do(func() {
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil {
			p.baseErr = err
			return
		}
		p.baseSTS = sts.NewFromConfig(cfg)
	})
	if p.baseErr != nil {
		return nil, p.baseErr
	}
	return p.baseSTS, nil
}

// staticCredentials envuelve credenciales ya obtenidas como CredentialsProvider.
type staticCredentials struct{ creds awssdk.Credentials }

func (s staticCredentials) Retrieve(context.Context) (awssdk.Credentials, error) { return s.creds, nil }

// sessionName arma un RoleSessionName válido ([\w+=,.@-]{2,64}) que identifique la cuenta.
func sessionName(acc provider.CloudAccount) string {
	id := acc.ID
	if id == "" {
		id = acc.Name
	}
	var b strings.Builder
	b.WriteString("rampa-verify-")
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '@':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := b.String()
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// describeErr resume un error de AWS sin volcar credenciales ni requests.
func describeErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "tiempo de espera agotado hablando con STS"
	}
	return err.Error()
}

// shellQuote protege un valor para pegarlo en una shell POSIX.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
