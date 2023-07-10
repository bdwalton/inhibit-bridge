# inhibit-bridge

This is a little utility to bridge between dbus org.freedesktop.ScreenSaver
Inhibit/UnInhibit messages and systemd's logind idle inhibits. The usecase is to
provide environments like i3/sway a mechanism to inhibit the screensaver when
browsers and the like are playing video. It relies on other tools like swayidle
to respect systemd's logind inhibits.

It provides a visual indicator in the status tray (if present) as to whether it
is currently holding any inhibits. Green for none held, yellow for programmatic
inhibits and red if there is a manual inhibit. A manual inhibit can be placed
via the menu available from the systray status icon. If not systray is present,
the tool will still function but status won't be visible.

You can toggle inhibit-bridge's manual inhibit state by sending SIGUSR1. This
allows you to wire up hotkeys in sway/i3/whatever to change the inhibit state
without the mouse.

inhibit-bridle will heartbeat check peers that have requested programatic
inhibits so that it doesn't leave the machine in an uninhibited state in the
case where the requesting peer program has crashed.

It works with recent versions of both Chrome, Firefox and vlc. In Firefox, you
may need to enable dom.wakelock.enabled in about:config so that dbus messages
are sent.

## License

inhibit-bridge is available under the Simplified BSD License; see LICENSE for
the full text.
