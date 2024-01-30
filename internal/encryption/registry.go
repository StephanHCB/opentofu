package encryption

import (
	"github.com/hashicorp/hcl/v2"
)

// Registry is a holder of KeyProvider and Method implementations. Key providers and methods can register
// themselves with this registry. You can call the Configure function to parse an HCL block as configuration.
type Registry struct {
	KeyProviders map[string]KeyProviderDefinition
	Methods      map[string]MethodDefinition
}

func NewRegistry() Registry {
	return Registry{
		KeyProviders: make(map[string]KeyProviderDefinition),
		Methods:      make(map[string]MethodDefinition),
	}
}

type DefinitionSchema struct {
	BodySchema        *hcl.BodySchema
	KeyProviderFields []string
}

type KeyProviderDefinition interface {
	Schema() DefinitionSchema
	Configure(*hcl.BodyContent, map[string]KeyProvider) (KeyProvider, hcl.Diagnostics)
}

type KeyProvider func() ([]byte, error)

type MethodDefinition interface {
	Schema() DefinitionSchema
	Configure(*hcl.BodyContent, map[string]KeyProvider) (Method, hcl.Diagnostics)
}

type Method interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}
