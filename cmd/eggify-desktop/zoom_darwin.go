//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

static WKWebView* eggifyFindWebView(NSView* v) {
	if ([v isKindOfClass:[WKWebView class]]) return (WKWebView*)v;
	for (NSView* s in v.subviews) {
		WKWebView* r = eggifyFindWebView(s);
		if (r) return r;
	}
	return nil;
}

// Page zoom (what Safari's ⌘+ does): the layout viewport shrinks and the page
// reflows to the window. Wails' own ZoomIn uses setMagnification, which scales
// the rendered page like a pinch and leaves it larger than the window.
static void eggifySetPageZoom(void* nsWindow, double zoom) {
	NSWindow* win = (__bridge NSWindow*)nsWindow;
	dispatch_async(dispatch_get_main_queue(), ^{
		WKWebView* wv = eggifyFindWebView(win.contentView);
		if (!wv) return;
		wv.magnification = 1.0;
		if (@available(macOS 11.0, *)) wv.pageZoom = zoom;
	});
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

// applyZoom zooms the window's web content; zoom 1 is 100%.
func applyZoom(w *application.WebviewWindow, zoom float64) {
	if w == nil {
		return
	}
	if p := w.NativeWindow(); p != nil {
		C.eggifySetPageZoom(p, C.double(zoom))
	}
}
