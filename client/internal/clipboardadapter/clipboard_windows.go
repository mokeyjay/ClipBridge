//go:build windows

package clipboardadapter

// Native Windows clipboard support for the content types golang.design/x/clipboard
// does not cover: files (CF_HDROP), RTF and HTML. Text and PNG images still go
// through golang.design (ReadText/ReadImage). This is pure-Go syscall (no cgo) so
// the client cross-compiles CGO-free. The patterns mirror golang.design's
// clipboard_windows.go (OpenClipboard on a locked OS thread, GlobalLock, etc.).

import (
	"context"
	"encoding/binary"
	"errors"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                         = syscall.NewLazyDLL("user32.dll")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")

	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procGlobalAlloc = kernel32.NewProc("GlobalAlloc")
	procGlobalFree  = kernel32.NewProc("GlobalFree")
	procGlobalLock  = kernel32.NewProc("GlobalLock")
	procGlobalUnlk  = kernel32.NewProc("GlobalUnlock")
	procGlobalSize  = kernel32.NewProc("GlobalSize")

	shell32            = syscall.NewLazyDLL("shell32.dll")
	procDragQueryFileW = shell32.NewProc("DragQueryFileW")
)

// Standard predefined clipboard format ids and GlobalAlloc flag.
const (
	cfUnicodeText uintptr = 13
	cfDIB         uintptr = 8
	cfDIBV5       uintptr = 17
	cfHDROP       uintptr = 15
	gmemMoveable  uintptr = 0x0002
)

// watchInterval is the clipboard change-counter poll period.
const watchInterval = 500 * time.Millisecond

// Registered (named) formats are resolved once and cached.
var (
	rtfOnce, htmlOnce sync.Once
	rtfFmt, htmlFmt   uintptr
)

func cfRTF() uintptr {
	rtfOnce.Do(func() { rtfFmt = registerFormat("Rich Text Format") })
	return rtfFmt
}
func cfHTML() uintptr {
	htmlOnce.Do(func() { htmlFmt = registerFormat("HTML Format") })
	return htmlFmt
}

// registerFormat resolves a named clipboard format id (0 on failure).
func registerFormat(name string) uintptr {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
	return r
}

// WatchClipboard polls the clipboard sequence number and emits one typed event
// per change, choosing the highest-priority content present (file > RTF > HTML >
// image > text). Text and image payloads reuse golang.design via ReadText/ReadImage.
func (s *System) WatchClipboard(ctx context.Context) <-chan ClipEvent {
	out := make(chan ClipEvent, 1)
	go func() {
		defer close(out)
		last, _, _ := procGetClipboardSequenceNumber.Call()
		ticker := time.NewTicker(watchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cur, _, _ := procGetClipboardSequenceNumber.Call()
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

// readTop reads the highest-priority clipboard content into a ClipEvent. Format
// detection uses IsClipboardFormatAvailable (no open needed); native payloads
// (file/RTF/HTML) are read under an OpenClipboard session, while text/image reuse
// golang.design (which opens/closes the clipboard itself) outside that session.
func (s *System) readTop() (ClipEvent, bool) {
	switch {
	case formatAvailable(cfHDROP):
		var path string
		var ok bool
		_ = withClipboard(func() error { path, ok = readDropFirstPath(); return nil })
		if ok && path != "" {
			return ClipEvent{Kind: KindFile, FilePath: path}, true
		}
	case formatAvailable(cfRTF()):
		var raw []byte
		var ok bool
		_ = withClipboard(func() error { raw, ok = readClipboardBytes(cfRTF()); return nil })
		if ok {
			return ClipEvent{Kind: KindRichText, Rich: trimTrailingNUL(raw), RichFormat: "rtf", Plain: s.plainText()}, true
		}
	case formatAvailable(cfHTML()):
		var raw []byte
		var ok bool
		_ = withClipboard(func() error { raw, ok = readClipboardBytes(cfHTML()); return nil })
		if ok {
			frag := unwrapCFHTML(string(trimTrailingNUL(raw)))
			return ClipEvent{Kind: KindRichText, Rich: []byte(frag), RichFormat: "html", Plain: s.plainText()}, true
		}
	case formatAvailable(cfDIBV5) || formatAvailable(cfDIB):
		if img, ok := s.ReadImage(); ok {
			return ClipEvent{Kind: KindImage, Image: img}, true
		}
	case formatAvailable(cfUnicodeText):
		if t, ok := s.ReadText(); ok {
			return ClipEvent{Kind: KindText, Text: t}, true
		}
	}
	return ClipEvent{}, false
}

// plainText returns the clipboard's plain text (for the rich-text fallback),
// reusing golang.design. Empty when absent.
func (s *System) plainText() string {
	t, _ := s.ReadText()
	return t
}

// WriteRichText writes rich content (format "rtf" | "html") plus a plain-text
// fallback (CF_UNICODETEXT) so apps that can't take rich text still paste.
func (s *System) WriteRichText(format string, rich []byte, plain string) error {
	return withClipboard(func() error {
		procEmptyClipboard.Call()
		if format == "html" {
			payload := append([]byte(wrapCFHTML(string(rich))), 0)
			if err := setClipboardGlobal(cfHTML(), payload); err != nil {
				return err
			}
		} else {
			payload := append(append([]byte(nil), rich...), 0) // NUL-terminate RTF
			if err := setClipboardGlobal(cfRTF(), payload); err != nil {
				return err
			}
		}
		if plain != "" {
			_ = setClipboardGlobal(cfUnicodeText, utf16Bytes(plain))
		}
		return nil
	})
}

// WriteFile places a file reference (CF_HDROP) on the clipboard so the file can
// be pasted in Explorer and other apps.
func (s *System) WriteFile(path string) error {
	wide, err := syscall.UTF16FromString(path) // includes a trailing NUL
	if err != nil {
		return err
	}
	const dropfilesSize = 20 // DWORD pFiles; POINT pt; BOOL fNC; BOOL fWide
	total := dropfilesSize + (len(wide)+1)*2
	return withClipboard(func() error {
		procEmptyClipboard.Call()
		h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(total))
		if h == 0 {
			return errors.New("GlobalAlloc 失败")
		}
		p, _, _ := procGlobalLock.Call(h)
		if p == 0 {
			procGlobalFree.Call(h)
			return errors.New("GlobalLock 失败")
		}
		base := unsafe.Pointer(p)
		*(*uint32)(base) = dropfilesSize     // pFiles: offset to the file list
		*(*uint32)(unsafe.Add(base, 16)) = 1 // fWide: paths are UTF-16
		dst := unsafe.Slice((*uint16)(unsafe.Add(base, dropfilesSize)), len(wide)+1)
		copy(dst, wide)
		dst[len(wide)] = 0 // second NUL terminates the file list
		procGlobalUnlk.Call(h)
		r, _, _ := procSetClipboardData.Call(cfHDROP, h)
		if r == 0 {
			procGlobalFree.Call(h)
			return errors.New("SetClipboardData 失败")
		}
		return nil
	})
}

// withClipboard opens the clipboard on a locked OS thread (bounded retry), runs
// fn, then closes it. OpenClipboard/sequence-number calls must share a thread.
func withClipboard(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	opened := false
	for i := 0; i < 20; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			opened = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !opened {
		return errors.New("无法打开剪贴板")
	}
	defer procCloseClipboard.Call()
	return fn()
}

// formatAvailable reports whether a clipboard format is currently present.
func formatAvailable(format uintptr) bool {
	if format == 0 {
		return false
	}
	r, _, _ := procIsClipboardFormatAvailable.Call(format)
	return r != 0
}

// readClipboardBytes copies the raw bytes of a clipboard format. Must be called
// within withClipboard.
func readClipboardBytes(format uintptr) ([]byte, bool) {
	h, _, _ := procGetClipboardData.Call(format)
	if h == 0 {
		return nil, false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, false
	}
	defer procGlobalUnlk.Call(h)
	sz, _, _ := procGlobalSize.Call(h)
	if sz == 0 {
		return nil, false
	}
	b := make([]byte, int(sz))
	copy(b, unsafe.Slice((*byte)(unsafe.Pointer(p)), int(sz)))
	return b, true
}

// readDropFirstPath returns the first file path from a CF_HDROP. Must be called
// within withClipboard.
func readDropFirstPath() (string, bool) {
	h, _, _ := procGetClipboardData.Call(cfHDROP)
	if h == 0 {
		return "", false
	}
	// With a nil buffer, DragQueryFileW returns the required char count for file 0.
	n, _, _ := procDragQueryFileW.Call(h, 0, 0, 0)
	if n == 0 {
		return "", false
	}
	buf := make([]uint16, n+1)
	r, _, _ := procDragQueryFileW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}

// setClipboardGlobal allocates a moveable HGLOBAL, copies raw into it and hands it
// to SetClipboardData. On success the system owns the memory; on failure it is
// freed. Must be called within withClipboard after EmptyClipboard.
func setClipboardGlobal(format uintptr, raw []byte) error {
	if format == 0 {
		return errors.New("未知剪贴板格式")
	}
	if len(raw) == 0 {
		raw = []byte{0}
	}
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(raw)))
	if h == 0 {
		return errors.New("GlobalAlloc 失败")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return errors.New("GlobalLock 失败")
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), len(raw)), raw)
	procGlobalUnlk.Call(h)
	if r, _, _ := procSetClipboardData.Call(format, h); r == 0 {
		procGlobalFree.Call(h)
		return errors.New("SetClipboardData 失败")
	}
	return nil
}

// utf16Bytes encodes s as little-endian UTF-16 with a trailing NUL (for CF_UNICODETEXT).
func utf16Bytes(s string) []byte {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		u = []uint16{0}
	}
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return b
}

// trimTrailingNUL drops trailing NUL bytes (RTF/HTML payloads are often NUL-terminated).
func trimTrailingNUL(b []byte) []byte {
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return b
}
