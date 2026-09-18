package turnstate

import (
	"net/http"
	"testing"
	"time"
)

func resetForTest(t *testing.T) {
	t.Helper()
	mu.Lock()
	origins = make(map[string]origin)
	writes = 0
	mu.Unlock()
	previous := nowFunc
	nowFunc = time.Now
	t.Cleanup(func() { nowFunc = previous })
}

func TestStripForeignKeepsSameCredentialAndDropsTheRest(t *testing.T) {
	resetForTest(t)

	Record(http.Header{Header: []string{"value-a"}}, "auth-a")

	kept, stripped := StripForeign(http.Header{Header: []string{"value-a"}}, "auth-a")
	if stripped {
		t.Fatalf("same credential must keep the header")
	}
	if kept.Get(Header) != "value-a" {
		t.Fatalf("header = %q, want value-a", kept.Get(Header))
	}

	other, stripped := StripForeign(http.Header{Header: []string{"value-a"}}, "auth-b")
	if !stripped {
		t.Fatalf("other credential must strip the header")
	}
	if other.Get(Header) != "" {
		t.Fatalf("header = %q, want removed", other.Get(Header))
	}

	unknown, stripped := StripForeign(http.Header{Header: []string{"value-unknown"}}, "auth-a")
	if !stripped {
		t.Fatalf("unknown value must fail closed")
	}
	if unknown.Get(Header) != "" {
		t.Fatalf("header = %q, want removed", unknown.Get(Header))
	}
}

func TestStripForeignIgnoresMissingHeaderAndBlankAuth(t *testing.T) {
	resetForTest(t)

	headers, stripped := StripForeign(http.Header{"Other": []string{"x"}}, "auth-a")
	if stripped || headers.Get("Other") != "x" {
		t.Fatalf("missing header must stay untouched")
	}

	Record(http.Header{Header: []string{"value-a"}}, "auth-a")
	if _, stripped := StripForeign(http.Header{Header: []string{"value-a"}}, "  "); stripped {
		t.Fatalf("blank auth must stay untouched")
	}
}

func TestStripForeignDropsExpiredOrigin(t *testing.T) {
	resetForTest(t)

	base := time.Now()
	nowFunc = func() time.Time { return base }
	Record(http.Header{Header: []string{"value-a"}}, "auth-a")

	nowFunc = func() time.Time { return base.Add(originTTL + time.Second) }
	if _, stripped := StripForeign(http.Header{Header: []string{"value-a"}}, "auth-a"); !stripped {
		t.Fatalf("expired origin must fail closed")
	}
}

func TestRecordPrunesExpiredOrigins(t *testing.T) {
	resetForTest(t)

	base := time.Now()
	nowFunc = func() time.Time { return base }
	Record(http.Header{Header: []string{"old"}}, "auth-a")

	nowFunc = func() time.Time { return base.Add(originTTL + time.Second) }
	for i := 0; i < pruneEvery; i++ {
		Record(http.Header{Header: []string{"filler"}}, "auth-a")
	}

	mu.Lock()
	_, stillThere := origins["old"]
	mu.Unlock()
	if stillThere {
		t.Fatalf("expired origin must be pruned")
	}
}
