package search

// MaskKey returns key with its middle characters hidden, keeping only the
// first 6 and last 6 characters visible. Keys of 12 characters or fewer are
// masked entirely, since a 6+6 split would either overlap or reveal the
// whole value.
func MaskKey(key string) string {
	const visiblePrefix = 6
	const visibleSuffix = 6
	if len(key) <= visiblePrefix+visibleSuffix {
		return "***"
	}
	return key[:visiblePrefix] + "***" + key[len(key)-visibleSuffix:]
}
