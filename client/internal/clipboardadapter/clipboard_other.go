//go:build !darwin && !windows

package clipboardadapter

import (
	"context"

	"golang.design/x/clipboard"
)

// WatchClipboard emits text and image change events via golang.design. Rich text
// (RTF/HTML) and file URLs are macOS-only, so non-darwin builds report only
// Text and Image kinds. Ends when ctx is cancelled (both source channels close).
func (s *System) WatchClipboard(ctx context.Context) <-chan ClipEvent {
	out := make(chan ClipEvent, 1)
	txt := clipboard.Watch(ctx, clipboard.FmtText)
	img := clipboard.Watch(ctx, clipboard.FmtImage)
	go func() {
		defer close(out)
		for txt != nil || img != nil {
			select {
			case <-ctx.Done():
				return
			case b, ok := <-txt:
				if !ok {
					txt = nil
					break
				}
				emit(ctx, out, ClipEvent{Kind: KindText, Text: string(b)})
			case b, ok := <-img:
				if !ok {
					img = nil
					break
				}
				emit(ctx, out, ClipEvent{Kind: KindImage, Image: b})
			}
		}
	}()
	return out
}

// emit sends an event unless ctx is cancelled first.
func emit(ctx context.Context, out chan<- ClipEvent, ev ClipEvent) {
	select {
	case out <- ev:
	case <-ctx.Done():
	}
}

// WriteRichText is a no-op off macOS (rich clipboard types are macOS-only).
func (s *System) WriteRichText(format string, rich []byte, plain string) error { return nil }

// WriteFile is a no-op off macOS (file-URL clipboard write-back is macOS-only).
func (s *System) WriteFile(path string) error { return nil }
