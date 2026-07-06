// Package clipboardadapter implements engine.Clipboard against the real system
// clipboard. Text and PNG images use golang.design/x/clipboard (cross-platform);
// rich text (RTF/HTML) and file references use the native macOS NSPasteboard via
// cgo (see clipboard_darwin.go). A single WatchClipboard channel reports typed
// changes; non-darwin builds (clipboard_other.go) report only text and images.
package clipboardadapter

import "golang.design/x/clipboard"

// ClipKind is the type of content a clipboard change carries.
type ClipKind int

const (
	KindNone ClipKind = iota
	KindText
	KindImage
	KindRichText
	KindFile
)

// ClipEvent is one typed clipboard change emitted by WatchClipboard. Only the
// fields relevant to Kind are populated.
type ClipEvent struct {
	Kind       ClipKind
	Text       string // KindText
	Image      []byte // KindImage (PNG)
	Rich       []byte // KindRichText (RTF or HTML bytes)
	RichFormat string // KindRichText: "rtf" | "html"
	Plain      string // KindRichText: plain-text fallback
	FilePath   string // KindFile: absolute path
}

// System is the OS-backed clipboard adapter.
type System struct{}

// New initializes the platform clipboard, returning an error if unavailable
// (the caller surfaces this as a persistent warning but keeps running).
func New() (*System, error) {
	if err := clipboard.Init(); err != nil {
		return nil, err
	}
	return &System{}, nil
}

// ReadText returns the current clipboard text and whether any text is present.
func (s *System) ReadText() (string, bool) {
	b := clipboard.Read(clipboard.FmtText)
	if len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// WriteText replaces the clipboard text. The underlying write is asynchronous;
// we do not wait on its completion channel.
func (s *System) WriteText(text string) error {
	clipboard.Write(clipboard.FmtText, []byte(text))
	return nil
}

// ReadImage returns the current clipboard PNG image bytes, if any.
func (s *System) ReadImage() ([]byte, bool) {
	b := clipboard.Read(clipboard.FmtImage)
	if len(b) == 0 {
		return nil, false
	}
	return b, true
}

// WriteImage places PNG image bytes on the clipboard.
func (s *System) WriteImage(png []byte) error {
	clipboard.Write(clipboard.FmtImage, png)
	return nil
}
