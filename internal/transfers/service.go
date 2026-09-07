package transfers

// Service owns provider-neutral transfer operations. Consumer-specific
// workflows live in child packages and receive this service as a dependency.
type Service struct {
	access      AccessPort
	multipart   MultipartPort
	scopes      ScopeReader
	credentials CredentialReader
	events      EventRecorder
}

func NewService(deps Dependencies) *Service {
	return &Service{
		access:      deps.Access,
		multipart:   deps.Multipart,
		scopes:      deps.Scopes,
		credentials: deps.Credentials,
		events:      deps.Events,
	}
}
