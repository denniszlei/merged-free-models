package httpx

import "testing"

func TestRewriteModelReplacesField(t *testing.T) {
	in := []byte(`{"model":"kilo/x-ai/foo:free","messages":[{"role":"user","content":"hi"}]}`)
	out := RewriteModel(in, "x-ai/foo:free")
	want := `"model":"x-ai/foo:free"`
	if !contains(out, want) {
		t.Fatalf("rewritten body missing %q: %s", want, out)
	}
	if contains(out, `kilo/x-ai/foo:free`) {
		t.Fatalf("original prefixed id should be gone: %s", out)
	}
}

func TestRewriteModelLeavesMalformedBodyAlone(t *testing.T) {
	bad := []byte(`not-json{`)
	if got := RewriteModel(bad, "anything"); string(got) != string(bad) {
		t.Fatalf("non-JSON body should pass through unchanged, got %s", got)
	}
}

func TestRewriteModelLeavesBodyAloneWithoutModelField(t *testing.T) {
	in := []byte(`{"input":"hi"}`)
	if got := RewriteModel(in, "anything"); string(got) != string(in) {
		t.Fatalf("body without model field should pass through, got %s", got)
	}
}

func TestWantsStream(t *testing.T) {
	if !WantsStream([]byte(`{"stream":true}`)) {
		t.Fatal("expected stream=true to be detected")
	}
	if WantsStream([]byte(`{"stream":false}`)) {
		t.Fatal("expected stream=false to be ignored")
	}
	if WantsStream([]byte(`{}`)) {
		t.Fatal("missing stream key should be false")
	}
}

func contains(b []byte, s string) bool {
	return indexOf(string(b), s) >= 0
}
func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
