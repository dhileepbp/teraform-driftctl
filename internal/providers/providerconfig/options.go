package providerconfig

// FetchOptions contains provider-specific discovery scopes.
type FetchOptions struct {
	Regions       []string
	Subscriptions []string
}
