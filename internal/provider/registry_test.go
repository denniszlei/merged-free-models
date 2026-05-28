package provider

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider struct {
	name        string
	models      []Model
	status      Status
	refreshHits int32
	lastOp      Op
	lastModel   string
}

func (f *fakeProvider) Name() string                       { return f.name }
func (f *fakeProvider) Models() []Model                    { return f.models }
func (f *fakeProvider) Status() Status                     { return f.status }
func (f *fakeProvider) Refresh(ctx context.Context) error  { atomic.AddInt32(&f.refreshHits, 1); return nil }
func (f *fakeProvider) Forward(w http.ResponseWriter, r *http.Request, op Op, originalModel string, body []byte) {
	f.lastOp = op
	f.lastModel = originalModel
	w.WriteHeader(http.StatusOK)
}

func TestRegistryLookupSplitsOnFirstSlash(t *testing.T) {
	kilo := &fakeProvider{name: "kilo"}
	oc := &fakeProvider{name: "opencode"}
	r := NewRegistry(time.Minute, kilo, oc)

	cases := []struct {
		in       string
		wantName string
		wantOrig string
		wantOK   bool
	}{
		{"kilo/x-ai/grok:free", "kilo", "x-ai/grok:free", true},
		{"opencode/claude-opus-4-7", "opencode", "claude-opus-4-7", true},
		{"kilo/", "", "", false},
		{"unknown/foo", "", "", false},
		{"no-prefix", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		p, orig, ok := r.Lookup(tc.in)
		if ok != tc.wantOK {
			t.Errorf("Lookup(%q) ok=%v want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if p.Name() != tc.wantName || orig != tc.wantOrig {
			t.Errorf("Lookup(%q)=(%s,%q) want (%s,%q)", tc.in, p.Name(), orig, tc.wantName, tc.wantOrig)
		}
	}
}

func TestRegistryMergesModelsInProviderOrder(t *testing.T) {
	a := &fakeProvider{name: "a", models: []Model{{ID: "a/1"}, {ID: "a/2"}}}
	b := &fakeProvider{name: "b", models: []Model{{ID: "b/1"}}}
	r := NewRegistry(time.Minute, a, b)
	got := r.Models()
	if len(got) != 3 || got[0].ID != "a/1" || got[2].ID != "b/1" {
		t.Fatalf("merged order wrong: %+v", got)
	}
}

func TestRegistryRefreshAllFanOut(t *testing.T) {
	a := &fakeProvider{name: "a"}
	b := &fakeProvider{name: "b"}
	r := NewRegistry(time.Minute, a, b)
	r.RefreshAll(context.Background())
	if atomic.LoadInt32(&a.refreshHits) != 1 || atomic.LoadInt32(&b.refreshHits) != 1 {
		t.Fatalf("each provider should be refreshed once; got a=%d b=%d", a.refreshHits, b.refreshHits)
	}
}
