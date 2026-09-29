package aws

import (
	"fmt"
	"regexp"
	"strings"
)

// roleARNPattern acepta ARNs de rol IAM en cualquier partición:
// arn:aws:iam::123456789012:role/Nombre, arn:aws-cn:..., arn:aws-us-gov:...
var roleARNPattern = regexp.MustCompile(`^arn:(aws|aws-cn|aws-us-gov):iam::(\d{12}):role/([\w+=,.@/-]+)$`)

// roleARN son las partes de un ARN de rol IAM que nos importan.
type roleARN struct {
	Partition string
	AccountID string
	Name      string
}

// parseRoleARN valida y descompone el ARN de un rol IAM.
func parseRoleARN(s string) (roleARN, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return roleARN{}, fmt.Errorf("el ARN del rol está vacío")
	}
	m := roleARNPattern.FindStringSubmatch(s)
	if m == nil {
		return roleARN{}, fmt.Errorf("ARN de rol IAM inválido: %q (esperaba arn:aws:iam::<cuenta de 12 dígitos>:role/<nombre>)", s)
	}
	return roleARN{Partition: m[1], AccountID: m[2], Name: m[3]}, nil
}

// dnsSuffix es el sufijo de dominio de los servicios de AWS según la partición.
func dnsSuffix(partition string) string {
	if partition == "aws-cn" {
		return "amazonaws.com.cn"
	}
	return "amazonaws.com"
}
