package server

import "github.com/kalbasit/ncps/pkg/upstreamcache"

type Server struct {
	upstreamCaches []upstreamcache.UpstreamCache
}
