package records

const (
	objectMethodRead   = "read"
	objectMethodCreate = "create"
	objectMethodUpdate = "update"
	objectMethodDelete = "delete"
)

// Service owns stateful record lookup and mutation operations. The value types
// remain in the parent objects package so this package depends on the domain
// model without creating a reverse dependency.
type Service struct {
	*queryService
	*mutationService
}

type queryService struct {
	recordReader    RecordReader
	aliases         AliasStore
	content         ContentReader
	checksumScope   ChecksumScopeQuery
	scope           ScopeQuery
	resources       OptionalResourceQuery
	pages           OptionalPageQuery
	urlPages        OptionalURLQuery
	authorizedQuery OptionalAuthorizedQuery
}

type mutationService struct {
	*queryService
	recordWriter  RecordWriter
	accessMethods AccessMethodWriter
	accessPolicy  AccessPolicyWriter
}

// Dependencies contains the consumer-owned repository ports used by Service.
// Optional query ports are optimization hooks; scan-based behavior remains
// available when they are absent.
type Dependencies struct {
	Reader        RecordReader
	Writer        RecordWriter
	AccessMethods AccessMethodWriter
	AccessPolicy  AccessPolicyWriter
	Aliases       AliasStore
	Content       ContentReader
	ChecksumScope ChecksumScopeQuery
	Scope         ScopeQuery
	Resources     OptionalResourceQuery
	Pages         OptionalPageQuery
	URLPages      OptionalURLQuery
	Authorized    OptionalAuthorizedQuery
}

// NewService composes the record service from repository ports.
func NewService(deps Dependencies) *Service {
	query := &queryService{
		recordReader:    deps.Reader,
		aliases:         deps.Aliases,
		content:         deps.Content,
		checksumScope:   deps.ChecksumScope,
		scope:           deps.Scope,
		resources:       deps.Resources,
		pages:           deps.Pages,
		urlPages:        deps.URLPages,
		authorizedQuery: deps.Authorized,
	}
	return &Service{
		queryService: query,
		mutationService: &mutationService{
			queryService:  query,
			recordWriter:  deps.Writer,
			accessMethods: deps.AccessMethods,
			accessPolicy:  deps.AccessPolicy,
		},
	}
}
