package clipboardadapter

import (
	"fmt"
	"strconv"
	"strings"
)

// CF_HTML ("HTML Format") on Windows wraps an HTML fragment in a text header that
// declares byte offsets (StartHTML/EndHTML/StartFragment/EndFragment). These two
// helpers build and parse that wrapper. They are pure (no syscalls) so they are
// compiled on every platform and unit-tested off Windows; the Windows clipboard
// adapter (clipboard_windows.go) uses them when reading/writing rich HTML.
// Reference: https://learn.microsoft.com/windows/win32/dataxchg/html-clipboard-format

const (
	cfHTMLPre  = "<html><body><!--StartFragment-->"
	cfHTMLPost = "<!--EndFragment--></body></html>"
	// Fixed-width %010d offsets keep the header length constant, avoiding a
	// circular dependency between the offsets and the header size.
	cfHTMLHeader = "Version:0.9\r\nStartHTML:%010d\r\nEndHTML:%010d\r\nStartFragment:%010d\r\nEndFragment:%010d\r\n"
)

// wrapCFHTML wraps an HTML fragment into the CF_HTML clipboard payload with
// correct byte offsets.
func wrapCFHTML(fragment string) string {
	hdrLen := len(fmt.Sprintf(cfHTMLHeader, 0, 0, 0, 0))
	startHTML := hdrLen
	startFragment := startHTML + len(cfHTMLPre)
	endFragment := startFragment + len(fragment)
	endHTML := endFragment + len(cfHTMLPost)
	return fmt.Sprintf(cfHTMLHeader, startHTML, endHTML, startFragment, endFragment) + cfHTMLPre + fragment + cfHTMLPost
}

// unwrapCFHTML extracts the HTML fragment from a CF_HTML payload, preferring the
// declared StartFragment/EndFragment offsets and falling back to the comment
// markers, then to the whole payload.
func unwrapCFHTML(data string) string {
	sf := cfHTMLOffset(data, "StartFragment:")
	ef := cfHTMLOffset(data, "EndFragment:")
	if sf >= 0 && ef > sf && ef <= len(data) {
		return data[sf:ef]
	}
	if i := strings.Index(data, "<!--StartFragment-->"); i >= 0 {
		i += len("<!--StartFragment-->")
		if j := strings.Index(data, "<!--EndFragment-->"); j >= i {
			return data[i:j]
		}
	}
	return data
}

// cfHTMLOffset reads the integer byte offset following a header label, or -1.
func cfHTMLOffset(data, label string) int {
	i := strings.Index(data, label)
	if i < 0 {
		return -1
	}
	i += len(label)
	j := i
	for j < len(data) && data[j] >= '0' && data[j] <= '9' {
		j++
	}
	n, err := strconv.Atoi(data[i:j])
	if err != nil {
		return -1
	}
	return n
}
