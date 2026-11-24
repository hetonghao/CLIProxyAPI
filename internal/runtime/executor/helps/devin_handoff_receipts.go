package helps

import (
	"crypto/sha256"
	"strings"
	"sync"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/cache"
)

// Codex persists mail before generation. A refused task and its later resend can
// appear adjacent, just like two messages first delivered in one call. Keep only
// digests of rejected batches; never keep task text or opaque payloads.
type DevinHandoffReceipts struct {
	init    sync.Once
	refused *cache.BoundedLRU[[32]byte, bool]
}

type handoffPeer struct{ author, recipient string }

func (r *DevinHandoffReceipts) Refuse(payload []byte, caller, session string) bool {
	var batch []ojson.Result
	ojson.GetBytesNoCopy(payload, "input").ForEach(func(_, item ojson.Result) bool {
		switch item.Get("type").String() {
		case "agent_message":
			batch = append(batch, item)
		case "message", "":
			role := item.Get("role").String()
			if role == "assistant" || role == "user" {
				batch = batch[:0]
			}
		case "function_call", "custom_tool_call", "tool_search_call", "function_call_output", "custom_tool_call_output", "tool_search_output", "output_text":
			batch = batch[:0]
		}
		return true
	})
	// For each sender, distinguish new opaque mail from already refused mail. A
	// partial resend from that sender cannot silently stand in for two messages.
	type state struct {
		outstanding, unreported int
		resending               bool
	}
	var peers map[handoffPeer]state
	var key [sha256.Size]byte
	for _, item := range batch {
		peer := handoffPeer{item.Get("author").String(), item.Get("recipient").String()}
		opaque, readable := false, false
		item.Get("content").ForEach(func(_, part ojson.Result) bool {
			switch part.Get("type").String() {
			case "encrypted_content":
				opaque = true
			case "input_text":
				readable = readable || strings.TrimSpace(part.Get("text").String()) != ""
			}
			return !opaque
		})
		if peers == nil {
			if !opaque {
				continue
			}
			peers = make(map[handoffPeer]state)
			key = hashHandoffPair(sha256.Sum256([]byte(caller)), sha256.Sum256([]byte(session)))
			r.init.Do(func() { r.refused = cache.NewBoundedLRU[[sha256.Size]byte, bool](1024, nil) })
		}
		s := peers[peer]
		if opaque {
			if peer.author == "" || peer.recipient == "" {
				return true
			}
			s.outstanding++
			s.unreported++
		} else if readable && s.outstanding > s.unreported {
			s.outstanding--
			s.resending = true
		}
		peers[peer] = s
		key = hashHandoffPair(key, sha256.Sum256([]byte(item.Raw)))
		if session != "" {
			if _, known := r.refused.Get(key); known {
				for k, s := range peers {
					s.unreported = 0
					s.resending = false
					peers[k] = s
				}
			}
		}
	}
	if peers == nil {
		return false
	}
	// The identical rejected prefix never qualifies on its own. A completely
	// resent peer may proceed independently of another peer's historical refusal.
	resumed := false
	blocked := false
	for _, s := range peers {
		resumed = resumed || s.resending
		blocked = blocked || s.unreported > 0 || (s.resending && s.outstanding > 0)
	}
	if resumed && !blocked {
		return false
	}
	if session != "" {
		r.refused.GetOrAdd(key, func() bool { return true })
	}
	return true
}

func hashHandoffPair(a, b [sha256.Size]byte) (sum [sha256.Size]byte) {
	hash := sha256.New()
	hash.Write(a[:])
	hash.Write(b[:])
	hash.Sum(sum[:0])
	return sum
}
