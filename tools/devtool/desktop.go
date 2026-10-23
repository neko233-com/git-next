// Copyright 2026 git-next contributors. MIT license.
package main

var desktopActivityCases = []string{
	"identity-profiles-unicode-cwd-language-atomic-settings",
	"native-window-wm-char-unicode-commit-and-pty",
	"native-clipboard-paste-with-original-preserved-in-memory",
	"native-keyboard-tabs-independent-shells-and-close",
	"native-window-resize-reaches-conpty",
	"embedded-icon-native-caption-hit-areas-and-compact-window",
	"border-drag-resizes-window-on-all-four-edges",
	"actual-gpui-gpu-window-chinese-and-color-screenshot",
	"native-mouse-selection-and-copy",
	"native-shift-page-and-mouse-wheel-scrollback",
	"tab-first-design-default-bash-live-command-highlighting",
	"owned-bash-and-native-cmd-real-gui-pty-exit",
	"explicit-command-exit-status-propagation",
	"bounded-output-drain-and-command-exit-without-ui-deadlock",
}

func init() {
	register("terminal-desktop-acceptance", "Exercise the owned Windows GPUI window with isolated profiles and clipboard restoration", "binary git output only-case paste-key", terminalDesktopAcceptance)
	register("windows-desktop-acceptance", "Compatibility alias for the owned terminal desktop acceptance", "binary git output only-case paste-key", terminalDesktopAcceptance)
}
