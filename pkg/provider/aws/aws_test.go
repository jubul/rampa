package aws

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"gopkg.in/yaml.v3"

	"rampa.dev/pkg/provider"
)

// fakeSTS es un cliente STS de mentira. Registra lo que le pidieron.
type fakeSTS struct {
	assumeErr   error
	identityErr error
	identityARN string

	gotRoleARN    string
	gotExternalID string
	gotSession    string
}

func (f *fakeSTS) AssumeRole(_ context.Context, in *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	f.gotRoleARN = awssdk.ToString(in.RoleArn)
	f.gotExternalID = awssdk.ToString(in.ExternalId)
	f.gotSession = awssdk.ToString(in.RoleSessionName)
	if f.assumeErr != nil {
		return nil, f.assumeErr
	}
	return &sts.AssumeRoleOutput{
		Credentials: &ststypes.Credentials{
			AccessKeyId:     awssdk.String("ASIAFAKE"),
			SecretAccessKey: awssdk.String("secret"),
			SessionToken:    awssdk.String("token"),
			Expiration:      awssdk.Time(time.Now().Add(15 * time.Minute)),
		},
		AssumedRoleUser: &ststypes.AssumedRoleUser{Arn: awssdk.String(f.identityARN)},
	}, nil
}

func (f *fakeSTS) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if f.identityErr != nil {
		return nil, f.identityErr
	}
	return &sts.GetCallerIdentityOutput{
		Arn:     awssdk.String(f.identityARN),
		Account: awssdk.String("123456789012"),
	}, nil
}

func newTestProvider(f *fakeSTS) *Provider {
	return New(Config{
		TrustedPrincipalARN: "arn:aws:iam::999999999999:role/rampa-api",
		STSClient:           f,
		NewAssumedClient:    func(awssdk.CredentialsProvider, string) STSClient { return f },
	})
}

func account() provider.CloudAccount {
	return provider.CloudAccount{
		ID:          "acc-1",
		Kind:        provider.AWS,
		Name:        "acme-prod",
		ExternalRef: "arn:aws:iam::123456789012:role/RampaConnect-RampaConnect",
		Region:      "us-east-1",
		Settings:    map[string]string{"external_id": "ext-abc123"},
	}
}

func TestVerifyOK(t *testing.T) {
	f := &fakeSTS{identityARN: "arn:aws:sts::123456789012:assumed-role/RampaConnect-RampaConnect/rampa-verify-acc-1"}
	p := newTestProvider(f)

	v, err := p.Verify(context.Background(), account())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !v.OK {
		t.Fatalf("esperaba OK, mensaje: %s", v.Message)
	}
	if v.Identity != f.identityARN {
		t.Errorf("identity = %q", v.Identity)
	}
	if f.gotRoleARN != account().ExternalRef {
		t.Errorf("AssumeRole con RoleArn %q", f.gotRoleARN)
	}
	if f.gotExternalID != "ext-abc123" {
		t.Errorf("AssumeRole con ExternalId %q", f.gotExternalID)
	}
	if f.gotSession != "rampa-verify-acc-1" {
		t.Errorf("RoleSessionName = %q", f.gotSession)
	}
}

func TestVerifyAssumeRoleDenied(t *testing.T) {
	f := &fakeSTS{assumeErr: errors.New("AccessDenied: User is not authorized to perform: sts:AssumeRole")}
	p := newTestProvider(f)

	v, err := p.Verify(context.Background(), account())
	if err != nil {
		t.Fatalf("un rechazo de AWS no es un error nuestro: %v", err)
	}
	if v.OK {
		t.Fatal("esperaba OK false")
	}
	if !strings.Contains(v.Message, "AccessDenied") || !strings.Contains(v.Message, "CREATE_COMPLETE") {
		t.Errorf("mensaje poco útil: %s", v.Message)
	}
}

func TestVerifyGetCallerIdentityFails(t *testing.T) {
	f := &fakeSTS{identityErr: errors.New("boom")}
	v, err := newTestProvider(f).Verify(context.Background(), account())
	if err != nil || v.OK {
		t.Fatalf("esperaba OK false sin error; ok=%v err=%v", v.OK, err)
	}
}

func TestVerifyInputErrors(t *testing.T) {
	f := &fakeSTS{identityARN: "x"}
	cases := map[string]struct {
		p   *Provider
		acc func(provider.CloudAccount) provider.CloudAccount
		msg string
	}{
		"sin principal": {
			p:   New(Config{STSClient: f}),
			acc: func(a provider.CloudAccount) provider.CloudAccount { return a },
			msg: EnvTrustedPrincipalARN,
		},
		"arn inválido": {
			p:   newTestProvider(f),
			acc: func(a provider.CloudAccount) provider.CloudAccount { a.ExternalRef = "not-an-arn"; return a },
			msg: "ARN de rol IAM inválido",
		},
		"sin external id": {
			p:   newTestProvider(f),
			acc: func(a provider.CloudAccount) provider.CloudAccount { a.Settings = nil; return a },
			msg: "external_id",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tc.p.Verify(context.Background(), tc.acc(account()))
			if err == nil {
				t.Fatal("esperaba error")
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error %q no menciona %q", err, tc.msg)
			}
			if f.gotRoleARN != "" {
				t.Error("no debería haber llamado a STS")
			}
		})
	}
}

func TestRegistry(t *testing.T) {
	p := newTestProvider(&fakeSTS{})
	cases := []struct {
		arn, region, want string
		wantErr           bool
	}{
		{"arn:aws:iam::123456789012:role/RampaConnect", "us-east-1", "123456789012.dkr.ecr.us-east-1.amazonaws.com", false},
		{"arn:aws:iam::123456789012:role/path/to/Role", "sa-east-1", "123456789012.dkr.ecr.sa-east-1.amazonaws.com", false},
		{"arn:aws-cn:iam::123456789012:role/R", "cn-north-1", "123456789012.dkr.ecr.cn-north-1.amazonaws.com.cn", false},
		{"arn:aws:iam::123456789012:role/R", "", "", true},
		{"arn:aws:iam::12345:role/R", "us-east-1", "", true},
		{"arn:aws:iam::123456789012:user/juan", "us-east-1", "", true},
		{"", "us-east-1", "", true},
		{"garbage", "us-east-1", "", true},
	}
	for _, tc := range cases {
		acc := account()
		acc.ExternalRef = tc.arn
		acc.Region = tc.region
		got, err := p.Registry(acc)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Registry(%q, %q) esperaba error, dio %q", tc.arn, tc.region, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Registry(%q, %q): %v", tc.arn, tc.region, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Registry(%q, %q) = %q, quería %q", tc.arn, tc.region, got, tc.want)
		}
	}
}

func TestConnectInstructionsWithTemplateURL(t *testing.T) {
	// Con la plantilla publicada en S3 hay link de quick-create y el comando usa --template-url hacia S3.
	const s3 = "https://rampa-public.s3.amazonaws.com/cloudformation/rampa-connect.yaml"
	p := New(Config{TrustedPrincipalARN: "arn:aws:iam::999999999999:role/rampa-api", DefaultRegion: "sa-east-1", TemplateURL: s3})
	ins := p.ConnectInstructions(provider.ConnectRequest{OrgSlug: "acme", ExternalID: "ext id/with+chars", CallbackURL: "https://api.rampa.dev/"})

	if ins.Title == "" || ins.Description == "" || ins.Command == "" {
		t.Fatal("instrucciones incompletas")
	}
	if !strings.Contains(ins.Description, "acme") {
		t.Error("la descripción debería mencionar la organización")
	}
	if len(ins.Fields) != 1 || ins.Fields[0].Name != "external_ref" || !ins.Fields[0].Required || ins.Fields[0].Label != "ARN del rol creado" {
		t.Errorf("fields = %+v", ins.Fields)
	}

	const prefix = "https://console.aws.amazon.com/cloudformation/home?region=sa-east-1#/stacks/quickcreate?"
	if !strings.HasPrefix(ins.URL, prefix) {
		t.Fatalf("URL = %s", ins.URL)
	}
	q, err := url.ParseQuery(strings.TrimPrefix(ins.URL, prefix))
	if err != nil {
		t.Fatalf("query inválida: %v", err)
	}
	want := map[string]string{
		"templateURL":               s3,
		"stackName":                 "RampaConnect",
		"param_ExternalId":          "ext id/with+chars",
		"param_TrustedPrincipalArn": "arn:aws:iam::999999999999:role/rampa-api",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, quería %q", k, q.Get(k), v)
		}
	}
	// La consola solo acepta S3: el link nunca debe apuntar a la API.
	if strings.Contains(q.Get("templateURL"), "api.rampa.dev") {
		t.Errorf("templateURL apunta a la API, CloudFormation lo rechaza: %s", q.Get("templateURL"))
	}
	// Nada sin escapar en el fragmento: la URL cruda no debe tener el ARN con ":" ni espacios.
	if strings.Contains(ins.URL, " ") || strings.Contains(ins.URL, "arn:aws") {
		t.Errorf("URL no está url-encoded: %s", ins.URL)
	}

	for _, s := range []string{"aws cloudformation create-stack", "--stack-name RampaConnect", "CAPABILITY_NAMED_IAM",
		"ParameterKey=ExternalId,ParameterValue='ext id/with+chars'", "ParameterKey=TrustedPrincipalArn,ParameterValue='arn:aws:iam::999999999999:role/rampa-api'",
		"--template-url '" + s3 + "'"} {
		if !strings.Contains(ins.Command, s) {
			t.Errorf("el comando no contiene %q:\n%s", s, ins.Command)
		}
	}
	if strings.Contains(ins.Command, "curl") || strings.Contains(ins.Command, "--template-body") {
		t.Errorf("con TemplateURL el comando no debería descargar la plantilla:\n%s", ins.Command)
	}
}

func TestConnectInstructionsWithoutTemplateURL(t *testing.T) {
	// Sin plantilla en S3 no hay quick-create (la consola no acepta URLs fuera
	// de S3): URL vacío y el comando baja la plantilla de la API y usa --template-body.
	p := New(Config{TrustedPrincipalARN: "arn:aws:iam::999999999999:role/rampa-api", DefaultRegion: "sa-east-1"})
	ins := p.ConnectInstructions(provider.ConnectRequest{OrgSlug: "acme", ExternalID: "ext-1", CallbackURL: "https://api.rampa.dev/"})

	if ins.URL != "" {
		t.Errorf("sin TemplateURL no debería haber link de quick-create: %s", ins.URL)
	}
	for _, s := range []string{
		"curl -fsSL -o rampa-connect.yaml 'https://api.rampa.dev/public/cloudformation/rampa-connect.yaml'",
		"aws cloudformation create-stack",
		"--region 'sa-east-1'",
		"--template-body file://rampa-connect.yaml",
		"--capabilities CAPABILITY_NAMED_IAM",
		"ParameterKey=ExternalId,ParameterValue='ext-1'",
	} {
		if !strings.Contains(ins.Command, s) {
			t.Errorf("el comando no contiene %q:\n%s", s, ins.Command)
		}
	}
	if strings.Contains(ins.Command, "--template-url") {
		t.Errorf("sin S3 no hay --template-url:\n%s", ins.Command)
	}
	if !strings.Contains(ins.Description, "https://api.rampa.dev/public/cloudformation/rampa-connect.yaml") || !strings.Contains(ins.Description, "S3") {
		t.Errorf("la descripción debería explicar el camino manual y por qué no hay botón: %s", ins.Description)
	}
	// La región de la cuenta gana sobre la default.
	ins = p.ConnectInstructions(provider.ConnectRequest{OrgSlug: "acme", ExternalID: "ext-1", CallbackURL: "https://api.rampa.dev", Region: "eu-west-1"})
	if !strings.Contains(ins.Command, "--region 'eu-west-1'") {
		t.Errorf("región de la cuenta ignorada:\n%s", ins.Command)
	}
}

func TestNewFromEnvReadsTemplateURL(t *testing.T) {
	t.Setenv(EnvTrustedPrincipalARN, "arn:aws:iam::1:role/r")
	t.Setenv(EnvTemplateURL, "  https://b.s3.amazonaws.com/t.yaml  ")
	p := NewFromEnv()
	if p.cfg.TemplateURL != "https://b.s3.amazonaws.com/t.yaml" {
		t.Errorf("TemplateURL = %q", p.cfg.TemplateURL)
	}
	if ins := p.ConnectInstructions(provider.ConnectRequest{CallbackURL: "https://api"}); !strings.Contains(ins.URL, "quickcreate") {
		t.Errorf("con la variable seteada debería haber quick-create: %q", ins.URL)
	}
}

func TestCloudFormationTemplate(t *testing.T) {
	if len(CloudFormationTemplate) == 0 {
		t.Fatal("plantilla vacía")
	}
	var doc map[string]any
	if err := yaml.Unmarshal(CloudFormationTemplate, &doc); err != nil {
		t.Fatalf("la plantilla no es YAML válido: %v", err)
	}
	params, _ := doc["Parameters"].(map[string]any)
	for _, p := range []string{"ExternalId", "TrustedPrincipalArn"} {
		if _, ok := params[p]; !ok {
			t.Errorf("falta el parámetro %s", p)
		}
	}
	res, _ := doc["Resources"].(map[string]any)
	role, _ := res["RampaConnectRole"].(map[string]any)
	if role["Type"] != "AWS::IAM::Role" {
		t.Fatalf("RampaConnectRole = %v", role)
	}
	props, _ := role["Properties"].(map[string]any)
	trust, _ := props["AssumeRolePolicyDocument"].(map[string]any)
	stmts, _ := trust["Statement"].([]any)
	if len(stmts) != 1 {
		t.Fatalf("trust policy con %d statements", len(stmts))
	}
	st := stmts[0].(map[string]any)
	if st["Action"] != "sts:AssumeRole" {
		t.Errorf("trust action = %v", st["Action"])
	}
	cond, _ := st["Condition"].(map[string]any)
	eq, _ := cond["StringEquals"].(map[string]any)
	if _, ok := eq["sts:ExternalId"]; !ok {
		t.Error("la trust policy no exige sts:ExternalId")
	}
	outs, _ := doc["Outputs"].(map[string]any)
	if _, ok := outs["RoleArn"]; !ok {
		t.Error("falta el output RoleArn")
	}

	// Las acciones exigidas por el contrato tienen que estar en la política.
	raw := string(CloudFormationTemplate)
	for _, a := range []string{"ecr:DescribeRepositories", "ecr:CreateRepository", "ecr:DescribeRegistry", "ecr:GetAuthorizationToken",
		"ecr:ListImages", "ecr:DescribeImages", "ecr:TagResource", "eks:DescribeCluster", "eks:ListClusters",
		"secretsmanager:GetSecretValue", "secretsmanager:CreateSecret", "secretsmanager:PutSecretValue", "secretsmanager:DescribeSecret",
		"secretsmanager:TagResource", "sts:GetCallerIdentity", "repository/rampa/*", "secret:rampa/*"} {
		if !strings.Contains(raw, a) {
			t.Errorf("la plantilla no incluye %s", a)
		}
	}
	if strings.Contains(raw, "\u2014") {
		t.Error("la plantilla tiene em-dashes")
	}
}

func TestSecretStoreSpec(t *testing.T) {
	p := newTestProvider(&fakeSTS{})
	spec := p.SecretStoreSpec(account(), provider.ClusterInfo{Name: "eks-prod", Region: "us-west-2"})
	if spec.Name != "aws-secrets-manager" {
		t.Errorf("name = %s", spec.Name)
	}
	if spec.Object["apiVersion"] != "external-secrets.io/v1" || spec.Object["kind"] != "ClusterSecretStore" {
		t.Errorf("objeto = %v", spec.Object)
	}
	aws := spec.Object["spec"].(map[string]any)["provider"].(map[string]any)["aws"].(map[string]any)
	if aws["service"] != "SecretsManager" || aws["region"] != "us-west-2" {
		t.Errorf("aws = %v", aws)
	}
	ref := aws["auth"].(map[string]any)["jwt"].(map[string]any)["serviceAccountRef"].(map[string]any)
	if ref["name"] != "rampa-agent" || ref["namespace"] != "rampa-system" {
		t.Errorf("serviceAccountRef = %v", ref)
	}

	// Sin región en el cluster, usa la de la cuenta.
	spec = p.SecretStoreSpec(account(), provider.ClusterInfo{Name: "eks"})
	aws = spec.Object["spec"].(map[string]any)["provider"].(map[string]any)["aws"].(map[string]any)
	if aws["region"] != "us-east-1" {
		t.Errorf("region fallback = %v", aws["region"])
	}
}

func TestWorkloadIdentityAndModules(t *testing.T) {
	p := newTestProvider(&fakeSTS{})
	ann := p.WorkloadIdentityAnnotations(account(), "arn:aws:iam::123456789012:role/rampa-checkout-api-prod")
	if ann["eks.amazonaws.com/role-arn"] != "arn:aws:iam::123456789012:role/rampa-checkout-api-prod" || len(ann) != 1 {
		t.Errorf("annotations = %v", ann)
	}
	if len(p.WorkloadIdentityAnnotations(account(), "")) != 0 {
		t.Error("sin identidad no debería anotar nada")
	}

	ref, ok := p.ServiceModule("postgres")
	if !ok || !strings.HasSuffix(ref.Source, "//terraform/modules/aws/postgres?ref=main") {
		t.Errorf("postgres: ok=%v ref=%s", ok, ref.Source)
	}
	for _, k := range []string{"redis", "sqs", "s3", "dynamodb", "cassandra"} {
		if _, ok := p.ServiceModule(k); ok {
			t.Errorf("%s no debería estar disponible todavía", k)
		}
	}
}

func TestRegisteredOnInit(t *testing.T) {
	p, ok := provider.Get(provider.AWS)
	if !ok {
		t.Fatal("aws no se registró en init")
	}
	if !p.Available() || p.DisplayName() != "Amazon Web Services" {
		t.Errorf("provider registrado raro: available=%v name=%s", p.Available(), p.DisplayName())
	}
}
