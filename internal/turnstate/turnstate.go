// Package turnstate tracks which credential minted each upstream
// X-Codex-Turn-State value, so a value replayed by a client is only ever
// forwarded to the same credential.
package turnstate

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Header is the Codex turn-state header exchanged with clients.
const Header = "X-Codex-Turn-State"

const (
	// originTTL bounds how long a minted value stays attributable. It matches
	// the one-hour turnover the turn-state plugin assumes for cached values.
	originTTL = time.Hour
	// pruneEvery triggers an expiry sweep after this many recorded values.
	pruneEvery = 512
)

type origin struct {
	authID  string
	expires time.Time
}

var (
	mu      sync.Mutex
	origins = make(map[string]origin)
	writes  uint64
	nowFunc = time.Now
)

// Record remembers that the upstream response minted the turn-state value in
// headers for authID. Values issued through paths that are not recorded here
// (WebSocket, image endpoints) or lost on restart stay unknown, and
// StripForeign fails closed for unknown values.
func Record(headers http.Header, authID string) {
	authID = strings.TrimSpace(authID)
	value := headerValue(headers)
	if value == "" || authID == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	origins[value] = origin{authID: authID, expires: nowFunc().Add(originTTL)}
	writes++
	if writes%pruneEvery == 0 {
		pruneExpiredLocked(nowFunc())
	}
}

// StripForeign returns headers unchanged when the turn-state value was minted
// for authID. Otherwise it returns a clone without the header, including when
// the value has no live origin record: a value minted on another instance, or
// before a restart, must not be replayed against a credential it may not
// belong to.
func StripForeign(headers http.Header, authID string) (http.Header, bool) {
	authID = strings.TrimSpace(authID)
	value := headerValue(headers)
	if value == "" || authID == "" {
		return headers, false
	}
	mu.Lock()
	record, ok := origins[value]
	mu.Unlock()
	if ok && record.authID == authID && nowFunc().Before(record.expires) {
		return headers, false
	}
	stripped := headers.Clone()
	stripped.Del(Header)
	return stripped, true
}

func headerValue(headers http.Header) string {
	if headers == nil {
		return ""
	}
	return strings.TrimSpace(headers.Get(Header))
}

func pruneExpiredLocked(now time.Time) {
	for value, record := range origins {
		if !now.Before(record.expires) {
			delete(origins, value)
		}
	}
}
