package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// remember keeps results of slow or rate-limited sources (weather, feeds,
// calendars) for a while, so the dashboard's refresh every few seconds does
// not reach them each time. When the source fails (e.g. Open-Meteo answering
// "overloaded"), the last good result stays on the tile for up to a day, and
// the source is asked again only after a short pause – also when there is no
// earlier result, so a struggling service is not asked on every refresh.
var remembered sync.Map

type rememberedValue struct {
	at  time.Time // when to count the ttl (or the pause) from
	got time.Time // when val was fetched
	val any
	err error // set while a failure is kept for the pause
}

const (
	rememberRetry    = time.Minute    // pause after a failure
	rememberKeepGood = 24 * time.Hour // how long an old result covers failures
)

// cacheKey adds whether the tile may reach the home network to a cache key.
func (in *Instance) cacheKey(key string) string {
	return fmt.Sprintf("%s|public=%v|%s", key, in.PublicOnly, in.credentials())
}

// credentials is a hash of the tile's settings and keys: what one tile looked
// up or logged in with is only reused by tiles with the same keys – never by a
// tile that merely names the same address (and has a wrong key, or none).
func (in *Instance) credentials() string {
	keys := make([]string, 0, len(in.Values))
	for k := range in.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k + "\x00" + in.Values[k] + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

func remember[T any](in *Instance, key string, ttl time.Duration, fetch func() (T, error)) (T, error) {
	key = in.cacheKey(key)
	var old *rememberedValue
	if v, ok := remembered.Load(key); ok {
		rv := v.(rememberedValue)
		old = &rv
		if rv.err != nil && time.Since(rv.at) < rememberRetry {
			var zero T
			return zero, rv.err
		}
		if rv.err == nil && time.Since(rv.at) < ttl {
			return rv.val.(T), nil
		}
	}
	val, err := fetch()
	switch {
	case errors.Is(err, context.Canceled):
		// The page was closed – nothing to learn about the source.
	case err == nil:
		remembered.Store(key, rememberedValue{at: time.Now(), got: time.Now(), val: val})
	case old != nil && old.val != nil && time.Since(old.got) < rememberKeepGood:
		// Keep showing the last good result; ask again after the pause.
		slog.Debug("Quelle fehlgeschlagen, zeige letzte Daten", "fehler", err)
		remembered.Store(key, rememberedValue{at: time.Now().Add(rememberRetry - ttl), got: old.got, val: old.val})
		return old.val.(T), nil
	default:
		remembered.Store(key, rememberedValue{at: time.Now(), err: err})
	}
	return val, err
}

// Sweep clears what the integrations keep between fetches: results nobody
// asked for in a day (a tile that was deleted or got another address) and,
// once a day, the logins and looked-up IDs of the services – a service in use
// just logs in again on its next fetch.
func Sweep(now time.Time) {
	remembered.Range(func(k, v any) bool {
		rv := v.(rememberedValue)
		last := rv.at
		if rv.got.After(last) {
			last = rv.got
		}
		if now.Sub(last) > rememberKeepGood {
			remembered.Delete(k)
		}
		return true
	})
	sweepMu.Lock()
	defer sweepMu.Unlock()
	if now.Sub(lastSessionSweep) < 24*time.Hour {
		return
	}
	if !lastSessionSweep.IsZero() {
		for _, m := range []*sync.Map{&sessions, &omvSessions, &ppTokens, &kavitaTokens, &embyServerIDs, &plexMachines,
			&iobrokerSimple, &shellyGen, &unifiPrefix, &arrLibraries, &lidarrLibraries, &readarrLibraries, &opnTraffic} {
			m.Clear()
		}
	}
	lastSessionSweep = now
}

var (
	sweepMu          sync.Mutex
	lastSessionSweep time.Time
)
