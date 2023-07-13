# inhibit-bridge

This is a little utility to bridge between dbus org.freedesktop.ScreenSaver
Inhibit/UnInhibit messages and systemd's logind idle inhibits. The usecase is to
provide environments like i3/sway a mechanism to inhibit the screensaver when
browsers and the like are playing video. It relies on other tools like swayidle
respecting systemd's logind inhibits.

It provides a visual indicator in the status tray (if present) as to whether it
is currently holding any inhibits. Green for none held, yellow for programmatic
inhibits and red if there is a manual inhibit in place. A manual inhibit can be
placed via the menu available from the systray status icon or toggled by sending
SIGUSR1. Using SIGUSR1 allows you to wire up hotkeys in sway/i3/whatever to
change the inhibit state without the mouse. If not systray is present, the tool
will still function but status won't be visible.

inhibit-bridge will heartbeat check peers that have requested programatic
inhibits so that it doesn't leave the machine in an inhibited state in the case
where the requesting peer program has crashed.

It works with recent versions of both Chrome, Firefox and vlc. In Firefox, you
may need to enable dom.wakelock.enabled in about:config so that dbus messages
are sent.

It accepts the following flags:
--heartbeat_interval - how often to check peers for liveness.
--logfile - where to write logs
--manual_inhibit_timeout - the duration for which manual inhibits are honoured
--notify - whether to send notifications of state changes in some cases
--verbose - whether to write logs

## License

inhibit-bridge is available under the Simplified BSD License; see LICENSE for
the full text.
