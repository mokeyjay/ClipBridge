//go:build darwin

package clipboardadapter

/*
#cgo CFLAGS: -mmacosx-version-min=10.13 -x objective-c
#cgo LDFLAGS: -framework Cocoa -mmacosx-version-min=10.13

#import <Cocoa/Cocoa.h>
#import <stdlib.h>
#import <string.h>

// cb_change_count returns the general pasteboard's monotonically increasing
// change counter; it bumps on every write by any process.
static long cb_change_count(void) {
	return (long)[[NSPasteboard generalPasteboard] changeCount];
}

// cb_top_kind classifies the highest-priority content currently on the
// pasteboard: 1=file URL, 2=RTF, 3=HTML, 4=PNG image, 5=plain text, 0=none.
// A copied Finder item (file URL) wins over any text representation it carries.
static int cb_top_kind(void) {
	NSPasteboard *pb = [NSPasteboard generalPasteboard];
	if ([pb canReadObjectForClasses:@[[NSURL class]]
	                        options:@{NSPasteboardURLReadingFileURLsOnlyKey: @YES}]) {
		return 1;
	}
	NSArray *types = [pb types];
	if ([types containsObject:NSPasteboardTypeRTF]) return 2;
	if ([types containsObject:NSPasteboardTypeHTML]) return 3;
	if ([types containsObject:NSPasteboardTypePNG]) return 4;
	if ([types containsObject:NSPasteboardTypeString]) return 5;
	return 0;
}

// cb_read_file_url_path returns a malloc'd UTF-8 filesystem path of the first
// file URL on the pasteboard, or NULL. The caller frees the result.
static char* cb_read_file_url_path(void) {
	NSPasteboard *pb = [NSPasteboard generalPasteboard];
	NSArray *urls = [pb readObjectsForClasses:@[[NSURL class]]
	                                  options:@{NSPasteboardURLReadingFileURLsOnlyKey: @YES}];
	if (urls.count == 0) return NULL;
	NSURL *u = urls[0];
	const char *p = [[u path] UTF8String];
	if (p == NULL) return NULL;
	return strdup(p);
}

// cb_read_data returns a malloc'd copy of the pasteboard data for a UTI and sets
// *outLen, or NULL/0 when absent. The caller frees the buffer.
static void* cb_read_data(const char* uti, int* outLen) {
	*outLen = 0;
	NSPasteboard *pb = [NSPasteboard generalPasteboard];
	NSString *t = [NSString stringWithUTF8String:uti];
	NSData *d = [pb dataForType:t];
	if (d == nil || d.length == 0) return NULL;
	void *buf = malloc(d.length);
	if (buf == NULL) return NULL;
	memcpy(buf, d.bytes, d.length);
	*outLen = (int)d.length;
	return buf;
}

// cb_write_file_url places a file URL on the pasteboard so it can be pasted
// (e.g. in Finder). Returns true on success.
static bool cb_write_file_url(const char* path) {
	NSPasteboard *pb = [NSPasteboard generalPasteboard];
	NSString *p = [NSString stringWithUTF8String:path];
	NSURL *u = [NSURL fileURLWithPath:p];
	if (u == nil) return false;
	[pb clearContents];
	return [pb writeObjects:@[u]];
}

// cb_write_rich declares both the rich UTI and plain string, writes the rich
// data, and sets the plain-text fallback so apps that can't take rich text
// still paste. declareTypes: clears the pasteboard and bumps changeCount.
static bool cb_write_rich(const char* uti, const void* buf, int len, const char* plain) {
	NSPasteboard *pb = [NSPasteboard generalPasteboard];
	NSString *t = [NSString stringWithUTF8String:uti];
	NSData *d = [NSData dataWithBytes:buf length:len];
	[pb declareTypes:@[t, NSPasteboardTypeString] owner:nil];
	bool ok = [pb setData:d forType:t];
	if (plain != NULL) {
		[pb setString:[NSString stringWithUTF8String:plain] forType:NSPasteboardTypeString];
	}
	return ok;
}
*/
import "C"

import (
	"context"
	"time"
	"unsafe"
)

// UTI type identifiers for rich content on the macOS pasteboard.
const (
	utiRTF  = "public.rtf"
	utiHTML = "public.html"
)

// watchInterval is how often the pasteboard change counter is polled. macOS has
// no change notification for arbitrary types, so polling is the standard
// approach; 500ms is responsive for clipboard use with negligible CPU cost.
const watchInterval = 500 * time.Millisecond

// WatchClipboard polls the pasteboard change counter and emits one typed event
// per change, choosing the highest-priority content present (file > RTF > HTML >
// image > text). Text and image payloads are read via golang.design (the proven
// path); rich text and file URLs use the native pasteboard. Ends on ctx cancel.
func (s *System) WatchClipboard(ctx context.Context) <-chan ClipEvent {
	out := make(chan ClipEvent, 1)
	go func() {
		defer close(out)
		last := int64(C.cb_change_count())
		ticker := time.NewTicker(watchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cur := int64(C.cb_change_count())
				if cur == last {
					continue
				}
				last = cur
				ev, ok := s.readTop()
				if !ok {
					continue
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// readTop reads the current highest-priority pasteboard content into a ClipEvent.
func (s *System) readTop() (ClipEvent, bool) {
	switch int(C.cb_top_kind()) {
	case 1: // file URL
		cp := C.cb_read_file_url_path()
		if cp == nil {
			return ClipEvent{}, false
		}
		path := C.GoString(cp)
		C.free(unsafe.Pointer(cp))
		if path == "" {
			return ClipEvent{}, false
		}
		return ClipEvent{Kind: KindFile, FilePath: path}, true
	case 2: // RTF
		if rich, ok := readData(utiRTF); ok {
			plain, _ := s.ReadText()
			return ClipEvent{Kind: KindRichText, Rich: rich, RichFormat: "rtf", Plain: plain}, true
		}
	case 3: // HTML
		if rich, ok := readData(utiHTML); ok {
			plain, _ := s.ReadText()
			return ClipEvent{Kind: KindRichText, Rich: rich, RichFormat: "html", Plain: plain}, true
		}
	case 4: // PNG image
		if img, ok := s.ReadImage(); ok {
			return ClipEvent{Kind: KindImage, Image: img}, true
		}
	case 5: // plain text
		if t, ok := s.ReadText(); ok {
			return ClipEvent{Kind: KindText, Text: t}, true
		}
	}
	return ClipEvent{}, false
}

// readData copies pasteboard data for a UTI (nil/empty → ok=false).
func readData(uti string) ([]byte, bool) {
	cuti := C.CString(uti)
	defer C.free(unsafe.Pointer(cuti))
	var n C.int
	buf := C.cb_read_data(cuti, &n)
	if buf == nil || n == 0 {
		return nil, false
	}
	b := C.GoBytes(buf, n)
	C.free(buf)
	return b, true
}

// WriteRichText writes rich content (format "rtf" | "html") with a plain-text
// fallback to the pasteboard. Unknown formats are treated as RTF.
func (s *System) WriteRichText(format string, rich []byte, plain string) error {
	uti := utiRTF
	if format == "html" {
		uti = utiHTML
	}
	cuti := C.CString(uti)
	defer C.free(unsafe.Pointer(cuti))
	var cplain *C.char
	if plain != "" {
		cplain = C.CString(plain)
		defer C.free(unsafe.Pointer(cplain))
	}
	var p unsafe.Pointer
	if len(rich) > 0 {
		p = unsafe.Pointer(&rich[0])
	}
	C.cb_write_rich(cuti, p, C.int(len(rich)), cplain)
	return nil
}

// WriteFile places a file reference (its path) on the pasteboard so the user can
// paste the file in Finder and other apps.
func (s *System) WriteFile(path string) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	C.cb_write_file_url(cpath)
	return nil
}
