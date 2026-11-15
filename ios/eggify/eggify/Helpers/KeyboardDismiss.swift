//
//  KeyboardDismiss.swift
//  eggify
//
//  Native keyboard dismissal: swipe the content down (as in Messages), tap
//  outside the field, or press Return in single-line fields.
//

import SwiftUI

extension View {
    /// Dragging the screen's lists, forms and scroll views moves the keyboard
    /// away with the finger. Applies to every scrollable view inside.
    func dismissesKeyboardOnScroll() -> some View {
        scrollDismissesKeyboard(.interactively)
    }
}
