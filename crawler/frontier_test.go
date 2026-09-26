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

	gotURL, gotDepth, viaNoFollow, ok := f.Pop()
	if !ok || gotURL != "https://example.com/a" || gotDepth != 0 {
		t.Fatalf("Pop() = %q, %d, %v; want /a, 0, true", gotURL, gotDepth, ok)
	}
	if viaNoFollow {
		t.Error("a URL pushed with Push should not be marked viaNoFollow")
	}

	if f.Push("https://example.com/a", 9) {
		t.Fatal("re-pushing an already-popped (visited) URL should fail")
	}

	gotURL, gotDepth, _, ok = f.Pop()
	if !ok || gotURL != "https://example.com/b" || gotDepth != 1 {
		t.Fatalf("Pop() = %q, %d, %v; want /b, 1, true", gotURL, gotDepth, ok)
	}

	if _, _, _, ok := f.Pop(); ok {
		t.Fatal("Pop() on empty frontier should return ok=false")
	}
	if f.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", f.Len())
	}
}

func TestFrontierPushNoFollowMarksURL(t *testing.T) {
	f := NewFrontier()

	if !f.PushNoFollow("https://example.com/a", 1) {
		t.Fatal("first PushNoFollow of /a should succeed")
	}
	if f.PushNoFollow("https://example.com/a", 1) {
		t.Fatal("re-pushing an already-queued URL should fail, even via PushNoFollow")
	}

	_, _, viaNoFollow, ok := f.Pop()
	if !ok {
		t.Fatal("Pop should succeed")
	}
	if !viaNoFollow {
		t.Error("a URL pushed with PushNoFollow should be marked viaNoFollow")
	}
}

func TestFrontierClearNoFollowRemovesMark(t *testing.T) {
	f := NewFrontier()

	if !f.PushNoFollow("https://example.com/a", 1) {
		t.Fatal("PushNoFollow should succeed")
	}
	f.ClearNoFollow("https://example.com/a")

	_, _, viaNoFollow, ok := f.Pop()
	if !ok {
		t.Fatal("Pop should succeed")
	}
	if viaNoFollow {
		t.Error("ClearNoFollow should have removed the mark before Pop")
	}
}

func TestFrontierClearNoFollowOnAlreadyPoppedIsNoOp(t *testing.T) {
	f := NewFrontier()
	f.Push("https://example.com/a", 0)
	f.Pop()
	// Must not panic, and must not resurrect the URL as queued.
	f.ClearNoFollow("https://example.com/a")
	if f.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", f.Len())
	}
}
