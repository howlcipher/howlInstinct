package receipt

import "net/url"

// SafeEndpoint reduces a provider URL to the part that identifies the service
// and discards every part that could carry a secret.
//
// A configured endpoint can legitimately contain credentials in userinfo
// (https://user:token@host/...) or in a query string (?api_key=...), and a
// receipt is a durable artifact that gets copied into audit trails and bug
// reports. Rebuilding the URL from scheme, host, and path is a positive
// allow-list: a credential in any other component cannot survive it, including
// in a component nobody has thought of yet.
//
// An unparseable URL yields the empty string rather than the original, because
// echoing back something we could not inspect would defeat the point.
func SafeEndpoint(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	safe := url.URL{
		Scheme: u.Scheme,
		Host:   u.Host, // Host excludes userinfo by construction.
		Path:   u.Path,
	}
	return safe.String()
}
