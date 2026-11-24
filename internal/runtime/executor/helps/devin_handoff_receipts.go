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
	var pending, previouslyRejected map[handoffPeer]bool
	var key [sha256.Size]byte
	for _, item := range batch {
		peer := handoffPeer{item.Get("author").String(), item.Get("recipient").String()}
		opaque, readable := false, false
		item.Get("content").ForEach(func(_, part ojson.Result) bool {
			switch part.Get("type").String() {
			case "encrypted_content":
				opaque = true
			case "input_text":
				if strings.TrimSpace(part.Get("text").String()) != "" {
					readable = true
				}
			}
			return !opaque
		})
		if pending == nil {
			if !opaque {
				continue
			}
			pending = make(map[handoffPeer]bool)
			previouslyRejected = make(map[handoffPeer]bool)
			// Length-independent separation of arbitrary caller and session identifiers.
			scope := [2][sha256.Size]byte{sha256.Sum256([]byte(caller)), sha256.Sum256([]byte(session))}
			key = hashHandoffPair(scope[0], scope[1])
			r.init.Do(func() { r.refused = cache.NewBoundedLRU[[sha256.Size]byte, bool](1024, nil) })
		}
		if opaque {
			if peer.author == "" || peer.recipient == "" {
				return true
			}
			pending[peer] = true
			delete(previouslyRejected, peer)
		} else if readable && previouslyRejected[peer] {
			delete(pending, peer)
			delete(previouslyRejected, peer)
		}
		key = hashHandoffPair(key, sha256.Sum256([]byte(item.Raw)))
		if session != "" {
			if _, ok := r.refused.Get(key); ok {
				for who := range pending {
					previouslyRejected[who] = true
				}
			}
		}
	}
	if len(pending) == 0 {
		return false
	}
	if session != "" {
		r.refused.GetOrAdd(key, func() bool { return true })
	}
	return true
}

func hashHandoffPair(a, b [sha256.Size]byte) [sha256.Size]byte {
	var pair [2 * sha256.Size]byte
	copy(pair[:], a[:])
	copy(pair[sha256.Size:], b[:])
	return sha256.Sum256(pair[:])
}
