# inhibit-bridge

This is a little utility to bridge between dbus org.freedesktop.ScreenSaver
Inhibit and UnInhibit messages to systemd's logind idle inhibits. The usecase is
to provide environments like i3/sway a mechanism to inhibit the screensaver when
browser and the like are playing video.  This relies on other tools like
swayidle respecting systemd's logind inhibits.

It provides a visual indicator in the status tray as to whether it is currently
holding any inhibits. Green for none held, yellow for programmatic inhibits and
red if there is a manual inhibit. Manual inhibits can be done via the menu on
the status icon and thus there is no need for (eg) waybar's idle_inhibitor
module. You must have a status tray listener available on dbus prior to startup
or the status tray will not be displayed. Other functionality should continue to
work.

inhibit-bridge handles SIGUSR1 by toggling the manual inhibit state. Using this,
you can wire up hotkeys in sway/i3/whatever to change the inhibit state without
the mouse.

inhibit-bridle will heartbeat check peers that have requested inhibits so that
we don't end up in a state where a program crash leads to permanently inhibited
screensaver/idle behaviour.

It works with recent versions of both Chrome, Firefox and vlc. In Firefox, you
may need to enable dom.wakelock.enabled in about:config so that dbus messages
are sent.

## License

inhibit-bridge is available under the Simplified BSD License; see LICENSE for
the full text.
