package crawler

import "testing"

func TestFrontierFIFOAndDedupe(t *testing.T) {
	f := NewFrontier()

	if !f.Push("https://example.com/a", 0) {
		t.Fatal("first push of /a should succeed")
	}
	if !f.Push("https://example.com/b", 1) {
		t.Fatal("first push of /b should succeed")
	}
	if f.Push("https://example.com/a", 5) {
		t.Fatal("re-pushing a queued URL should fail")
	}
	if f.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", f.Len())
	}

	gotURL, gotDepth, ok := f.Pop()
	if !ok || gotURL != "https://example.com/a" || gotDepth != 0 {
		t.Fatalf("Pop() = %q, %d, %v; want /a, 0, true", gotURL, gotDepth, ok)
	}

	if f.Push("https://example.com/a", 9) {
		t.Fatal("re-pushing an already-popped (visited) URL should fail")
	}

	gotURL, gotDepth, ok = f.Pop()
	if !ok || gotURL != "https://example.com/b" || gotDepth != 1 {
		t.Fatalf("Pop() = %q, %d, %v; want /b, 1, true", gotURL, gotDepth, ok)
	}

	if _, _, ok := f.Pop(); ok {
		t.Fatal("Pop() on empty frontier should return ok=false")
	}
	if f.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", f.Len())
	}
}
