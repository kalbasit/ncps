package database

import "github.com/kalbasit/ncps/ent"

// isEntNotFound reports whether err is Ent's *NotFoundError. Kept
// in a separate file so the ent import stays tight and isolated.
func isEntNotFound(err error) bool {
	return ent.IsNotFound(err)
}
