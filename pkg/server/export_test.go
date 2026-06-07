package server

// NarInfoErrorStatus is a test-only export of narInfoErrorStatus.
func NarInfoErrorStatus(err error) (status int, respond bool) {
	return narInfoErrorStatus(err)
}
