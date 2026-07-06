package clipboardadapter

import "testing"

// TestCFHTMLRoundTrip verifies wrap→unwrap returns the original HTML fragment and
// that the declared offsets point exactly at the fragment bytes.
func TestCFHTMLRoundTrip(t *testing.T) {
	for _, frag := range []string{
		"<b>hello</b>",
		"<p>带格式的中文 &amp; symbols</p>",
		"",
		"<a href=\"https://example.com\">link</a>",
	} {
		wrapped := wrapCFHTML(frag)
		// Offsets must slice out exactly the fragment.
		sf := cfHTMLOffset(wrapped, "StartFragment:")
		ef := cfHTMLOffset(wrapped, "EndFragment:")
		if sf < 0 || ef < sf || ef > len(wrapped) {
			t.Fatalf("bad offsets sf=%d ef=%d len=%d for %q", sf, ef, len(wrapped), frag)
		}
		if got := wrapped[sf:ef]; got != frag {
			t.Errorf("offset slice = %q, want %q", got, frag)
		}
		if got := unwrapCFHTML(wrapped); got != frag {
			t.Errorf("unwrap = %q, want %q", got, frag)
		}
	}
}

// TestCFHTMLHeaderConsistency checks StartHTML points at <html> and EndHTML at the end.
func TestCFHTMLHeaderConsistency(t *testing.T) {
	w := wrapCFHTML("<i>x</i>")
	sh := cfHTMLOffset(w, "StartHTML:")
	eh := cfHTMLOffset(w, "EndHTML:")
	if sh < 0 || w[sh:sh+len("<html>")] != "<html>" {
		t.Errorf("StartHTML does not point at <html> (sh=%d)", sh)
	}
	if eh != len(w) {
		t.Errorf("EndHTML = %d, want %d", eh, len(w))
	}
}

// TestUnwrapFallbackToMarkers verifies fragment extraction when offsets are absent.
func TestUnwrapFallbackToMarkers(t *testing.T) {
	raw := "Version:0.9\r\n<html><body><!--StartFragment--><b>hi</b><!--EndFragment--></body></html>"
	if got := unwrapCFHTML(raw); got != "<b>hi</b>" {
		t.Errorf("fallback unwrap = %q, want <b>hi</b>", got)
	}
}
