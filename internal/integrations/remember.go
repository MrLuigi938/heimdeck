package integrations

import (
	"fmt"
	"sync"
	"time"
)

// remember keeps results of slow or rate-limited sources (weather, feeds,
// calendars) for a while, so the dashboard's refresh every few seconds does
// not reach them each time. Failures are not kept.
var remembered sync.Map

type rememberedValue struct {
	at  time.Time
	val any
}

// cacheKey adds whether the tile may reach the home network to a cache key.
func (in *Instance) cacheKey(key string) string { return fmt.Sprintf("%s|public=%v", key, in.PublicOnly) }

func remember[T any](in *Instance, key string, ttl time.Duration, fetch func() (T, error)) (T, error) {
	key = in.cacheKey(key)
	if v, ok := remembered.Load(key); ok && time.Since(v.(rememberedValue).at) < ttl {
		return v.(rememberedValue).val.(T), nil
	}
	val, err := fetch()
	if err == nil {
		remembered.Store(key, rememberedValue{time.Now(), val})
	}
	return val, err
}
