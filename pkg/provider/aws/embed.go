package aws

import _ "embed"

// CloudFormationTemplate es la plantilla que crea el rol IAM RampaConnect en la
// cuenta del cliente. La API la sirve pública en
// GET /public/cloudformation/rampa-connect.yaml y el link de quick-create apunta ahí.
//
//go:embed cloudformation/rampa-connect.yaml
var CloudFormationTemplate []byte
