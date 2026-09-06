# Native macOS Fullscreen Design

## Goal

Enable the standard macOS fullscreen control for Telegram Companion without changing application version 0.8.2.

## Behaviour

- The green macOS window button enters native fullscreen.
- `Control + Command + F` uses the standard macOS fullscreen shortcut.
- `Escape` exits native fullscreen.
- Exiting fullscreen restores the previous maximised window state.
- The application continues to start maximised, not fullscreen.

## Implementation

Pass an explicit Wails `mac.Options` value with zoom enabled and Escape fullscreen exit enabled. Wails maps this to the native macOS window configuration, so no custom React control or key listener is required.

## Verification

Add a Go regression test for the Wails configuration, run desktop Go tests, build the frontend and Wails application, install it in `/Applications`, then verify the process starts.
