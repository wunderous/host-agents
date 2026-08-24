// Package resourceid defines the opaque, tenant-scoped entity identifiers
// exchanged by Host Agent capabilities. Provider coordinates are deliberately
// kept outside this package so the identifier remains stable across providers.
package resourceid

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	ErrInvalidURI    = errors.New("invalid resource URI")
	ErrUnknownType   = errors.New("unknown resource type")
	ErrForeignTenant = errors.New("resource URI belongs to a different tenant")
)

const (
	TypeVM              = "vm"
	TypeContainer       = "container"
	TypePod             = "pod"
	TypeHost            = "host"
	TypeCluster         = "cluster"
	TypePostgresService = "postgres-service"
	TypeTiDBService     = "tidb-service"
	TypeSQLiteDatabase  = "sqlite-database"
	TypeDatabase        = "database"
	TypeTunnel          = "tunnel"
	TypeLLMRuntime      = "llm-runtime"
	TypeModel           = "model"
	TypeHostService     = "host-service"
	TypeSQLConnector    = "sql-connector"
	TypeOCIRegistry     = "oci-registry"
	TypeServiceDomain   = "service-domain"
	TypeService         = "service"
	TypeNetwork         = "network"
	TypeStorage         = "storage"
	TypeImage           = "image"
	TypeProfile         = "profile"
	TypeK3s             = "k3s"
	TypeCloudflared     = "cloudflared"
	TypeLanguage        = "language"
	TypeEmbedding       = "embedding"
	TypeOperation       = "operation"
	TypePlan            = "plan"
)

var knownTypes = map[string]struct{}{
	TypeVM: {}, TypeContainer: {}, TypePod: {}, TypeHost: {}, TypeCluster: {},
	TypePostgresService: {}, TypeTiDBService: {}, TypeSQLiteDatabase: {}, TypeDatabase: {},
	TypeTunnel: {}, TypeLLMRuntime: {}, TypeModel: {}, TypeHostService: {},
	TypeSQLConnector: {}, TypeOCIRegistry: {}, TypeServiceDomain: {}, TypeService: {},
	TypeNetwork: {}, TypeStorage: {}, TypeImage: {}, TypeProfile: {}, TypeK3s: {},
	TypeCloudflared: {}, TypeLanguage: {}, TypeEmbedding: {}, TypeOperation: {}, TypePlan: {},
}

var segmentPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// URI is an opaque, canonical entity identity. ResourceID may contain colons
// (for example, an Ollama model reference) because parsing splits only twice.
type URI struct {
	ResourceType string
	TenantID     string
	ResourceID   string
}

// Record is the persistence-neutral registry projection. Coordinates are
// typed by the owning provider boundary and remain opaque to this package.
type Record struct {
	URI          string
	ResourceType string
	TenantID     string
	ResourceID   string
	Coordinates  map[string]any
	Status       string
	CreatedAt    string
	UpdatedAt    string
}

func (u URI) String() string {
	return u.ResourceType + ":" + u.TenantID + ":" + u.ResourceID
}

func (u URI) Validate() error {
	if _, ok := knownTypes[u.ResourceType]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownType, u.ResourceType)
	}
	if !segmentPattern.MatchString(u.ResourceType) || !segmentPattern.MatchString(u.TenantID) {
		return fmt.Errorf("%w: type and tenant must match [a-z][a-z0-9-]{0,31}", ErrInvalidURI)
	}
	if strings.TrimSpace(u.ResourceID) == "" || strings.ContainsAny(u.ResourceID, "\r\n\t ") {
		return fmt.Errorf("%w: resource id is empty or contains whitespace", ErrInvalidURI)
	}
	return nil
}

// IsKnownType is the single canonical resource-kind check used by catalog
// registration and URI parsing. Provider plugins may use only kinds declared
// here; a URI-shaped value is not a resource identity by itself.
func IsKnownType(resourceType string) bool {
	_, ok := knownTypes[strings.TrimSpace(resourceType)]
	return ok
}

func Parse(value string) (URI, error) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 3)
	if len(parts) != 3 {
		return URI{}, fmt.Errorf("%w: expected resource-type:tenant-id:resource-id", ErrInvalidURI)
	}
	u := URI{ResourceType: parts[0], TenantID: parts[1], ResourceID: parts[2]}
	if err := u.Validate(); err != nil {
		return URI{}, err
	}
	return u, nil
}

func New(resourceType, tenantID, resourceID string) (URI, error) {
	u := URI{ResourceType: strings.TrimSpace(resourceType), TenantID: strings.TrimSpace(tenantID), ResourceID: resourceID}
	if err := u.Validate(); err != nil {
		return URI{}, err
	}
	return u, nil
}

func VMURI(tenant, id string) (URI, error)            { return New(TypeVM, tenant, id) }
func ContainerURI(tenant, id string) (URI, error)     { return New(TypeContainer, tenant, id) }
func PodURI(tenant, id string) (URI, error)           { return New(TypePod, tenant, id) }
func HostURI(tenant, id string) (URI, error)          { return New(TypeHost, tenant, id) }
func ClusterURI(tenant, id string) (URI, error)       { return New(TypeCluster, tenant, id) }
func DatabaseURI(tenant, id string) (URI, error)      { return New(TypeDatabase, tenant, id) }
func TunnelURI(tenant, id string) (URI, error)        { return New(TypeTunnel, tenant, id) }
func LLMRuntimeURI(tenant, id string) (URI, error)    { return New(TypeLLMRuntime, tenant, id) }
func ModelURI(tenant, id string) (URI, error)         { return New(TypeModel, tenant, id) }
func HostServiceURI(tenant, id string) (URI, error)   { return New(TypeHostService, tenant, id) }
func SQLConnectorURI(tenant, id string) (URI, error)  { return New(TypeSQLConnector, tenant, id) }
func OCIRegistryURI(tenant, id string) (URI, error)   { return New(TypeOCIRegistry, tenant, id) }
func ServiceDomainURI(tenant, id string) (URI, error) { return New(TypeServiceDomain, tenant, id) }
func ServiceURI(tenant, id string) (URI, error)       { return New(TypeService, tenant, id) }

func KnownTypes() []string {
	values := make([]string, 0, len(knownTypes))
	for value := range knownTypes {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
