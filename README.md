# inhibit-bridge

This is a little utility to bridge between dbus org.freedesktop.ScreenSaver
Inhibit and UnInhibit messages to systemd's logind idle inhibits. The usecase is
to provide environments like i3/sway a mechanism to inhibit the screensaver when
browser and the like are playing video.  This relies on other tools like
swayidle respecting systemd's logind inhibits.

It is capable of heartbeat checking peers that requested inhibits so that we
don't end up in a state where a program crash leads to permanently inhibited
screensaver/idle behaviour.

It works with recent versions of both Chrome and Firefox. In firefox, you may
need to enable dom.wakelock.enabled in about:config so that dbus messages are
sent.

## License

inhibit-bridge is available under the Simplified BSD License; see LICENSE for
the full text.
