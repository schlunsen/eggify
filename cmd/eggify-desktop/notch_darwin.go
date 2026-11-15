//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// The display the theatre lives on: the one with a camera housing (the
// built-in display, even when an external one is primary), else the primary.
static NSScreen* eggifyNotchScreen(void) {
	if (@available(macOS 12.0, *)) {
		for (NSScreen* s in [NSScreen screens]) {
			if (s.safeAreaInsets.top > 0 && !NSIsEmptyRect(s.auxiliaryTopLeftArea) && !NSIsEmptyRect(s.auxiliaryTopRightArea)) return s;
		}
	}
	NSScreen* primary = [[NSScreen screens] firstObject];
	return primary ? primary : [NSScreen mainScreen];
}

// Housing width and menu bar height in points; hasNotch 0 without a housing.
static void eggifyNotchGeometry(int* hasNotch, double* notchWidth, double* menuHeight, double* centre) {
	NSScreen* s = eggifyNotchScreen();
	*hasNotch = 0; *notchWidth = 0; *menuHeight = 24; *centre = 0;
	if (!s) return;
	*centre = NSMidX(s.frame);
	double bar = NSMaxY(s.frame) - NSMaxY(s.visibleFrame);
	if (bar > 0) *menuHeight = bar;
	if (@available(macOS 12.0, *)) {
		NSRect l = s.auxiliaryTopLeftArea, r = s.auxiliaryTopRightArea;
		if (s.safeAreaInsets.top > 0 && !NSIsEmptyRect(l) && !NSIsEmptyRect(r) && NSMinX(r) > NSMaxX(l)) {
			*hasNotch = 1;
			*notchWidth = NSMinX(r) - NSMaxX(l);
			*menuHeight = s.safeAreaInsets.top;
			*centre = NSMaxX(l) + *notchWidth / 2.0;
		}
	}
}

// Puts the window's top edge at the top of the display, centred on the
// housing. setFrame on a borderless panel is not pushed below the menu bar.
static void eggifyPlaceNotch(void* nsWindow, double width, double height) {
	NSWindow* win = (__bridge NSWindow*)nsWindow;
	int has; double nw, mh, centre;
	eggifyNotchGeometry(&has, &nw, &mh, &centre);
	NSScreen* s = eggifyNotchScreen();
	if (!s) return;
	NSRect f = NSMakeRect(round(centre - width / 2.0), NSMaxY(s.frame) - height, width, height);
	[win setFrame:f display:YES];
	// Wails' webview fills the content view; keep it filling the new size.
	for (NSView* v in win.contentView.subviews) {
		v.frame = win.contentView.bounds;
		v.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
	}
	[win orderFrontRegardless];
}
*/
import "C"

import "github.com/wailsapp/wails/v3/pkg/application"

// readNotchGeometry measures the display the theatre goes on (main thread).
func readNotchGeometry() notchGeometry {
	var has C.int
	var nw, mh, centre C.double
	C.eggifyNotchGeometry(&has, &nw, &mh, &centre)
	return notchGeometry{HasNotch: has != 0, NotchWidth: float64(nw), MenuHeight: float64(mh)}
}

// placeNotchWindow sizes the window and pins it under the housing (main thread).
func placeNotchWindow(w *application.WebviewWindow, width, height int) {
	if w == nil {
		return
	}
	if p := w.NativeWindow(); p != nil {
		C.eggifyPlaceNotch(p, C.double(width), C.double(height))
	}
}
