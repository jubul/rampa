package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"

	"rampa.dev/pkg/provider"
)

// infraSessionDuration es lo que dura la sesión del runner de infraestructura.
// Un apply de EKS tarda entre 15 y 25 minutos; una hora deja margen y es el
// máximo por defecto de un rol IAM recién creado.
const infraSessionDuration = time.Hour

var _ provider.CredentialsProvider = (*Provider)(nil)

// Credentials asume el rol de la cuenta con su external ID y devuelve
// credenciales temporales de una hora para el runner de infraestructura. El
// nombre de sesión es rampa-infra-<run corto> cuando el contexto trae el id
// del run (provider.WithRunID), para que quede trazable en CloudTrail.
func (p *Provider) Credentials(ctx context.Context, acc provider.CloudAccount) (provider.Credentials, error) {
	if p.cfg.TrustedPrincipalARN == "" {
		return provider.Credentials{}, fmt.Errorf("provider aws: falta la identidad del plano de control: configurá %s con el ARN del rol o usuario IAM con el que corre la API", EnvTrustedPrincipalARN)
	}
	if _, err := parseRoleARN(acc.ExternalRef); err != nil {
		return provider.Credentials{}, fmt.Errorf("provider aws: cuenta %q: %w", acc.Name, err)
	}
	externalID := strings.TrimSpace(acc.Settings[SettingExternalID])
	if externalID == "" {
		return provider.Credentials{}, fmt.Errorf("provider aws: cuenta %q: falta settings.%s", acc.Name, SettingExternalID)
	}
	region := strings.TrimSpace(acc.Region)
	if region == "" {
		region = p.cfg.DefaultRegion
	}
	base, err := p.baseClient(ctx, region)
	if err != nil {
		return provider.Credentials{}, fmt.Errorf("provider aws: no pude armar el cliente STS del plano de control: %w", err)
	}
	assume := stscreds.NewAssumeRoleProvider(base, acc.ExternalRef, func(o *stscreds.AssumeRoleOptions) {
		o.ExternalID = awssdk.String(externalID)
		o.RoleSessionName = infraSessionName(provider.RunIDFrom(ctx))
		o.Duration = infraSessionDuration
	})
	creds, err := assume.Retrieve(ctx)
	if err != nil {
		return provider.Credentials{}, fmt.Errorf("no pude asumir el rol %s: %s. Revisá que el stack RampaConnect esté completo y que la cuenta siga verificada", acc.ExternalRef, describeErr(err))
	}
	return provider.Credentials{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
		Expires:         creds.Expires,
		Env: map[string]string{
			"AWS_REGION":         region,
			"AWS_DEFAULT_REGION": region,
		},
	}, nil
}

// infraSessionName arma "rampa-infra-<run corto>" con un RoleSessionName
// válido ([\w+=,.@-]{2,64}). Sin run id queda "rampa-infra".
func infraSessionName(runID string) string {
	short := runID
	if i := strings.Index(short, "-"); i > 0 {
		short = short[:i]
	}
	if len(short) > 12 {
		short = short[:12]
	}
	var b strings.Builder
	b.WriteString("rampa-infra")
	if short != "" {
		b.WriteByte('-')
		for _, r := range short {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '@':
				b.WriteRune(r)
			default:
				b.WriteRune('-')
			}
		}
	}
	return b.String()
}
