// Package local provides local (single-instance) lock implementations.
//
// These locks use standard Go sync primitives (sync.Mutex and sync.RWMutex)
// and are suitable for single-instance deployments. They ignore TTL
// parameters since local locks don't expire.
package local

const (
	// numShards is the number of mutex shards for lock striping.
	// This provides bounded memory usage while allowing concurrent locks for different keys.
	numShards = 1024
)
