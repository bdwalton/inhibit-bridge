# inhibit-bridge

This is a little utility to bridge between dbus org.freedesktop.ScreenSaver
Inhibit and UnInhibit messages to systemd's logind idle inhibits. The usecase is
for environments like i3/sway that aren't full desktop environments with all of
this kind of plumbing by default to have a facility to inhibit (eg) swayidle
from locking the screen when browsers are playing video.

It is capable of heartbeat checking peers that requested inhibits so that we
don't end up in a state where a program crash leads to permanently inhibited
screensaver/idle behaviour.

It works with recent versions of both Chrome and Firefox. (In firefox, I had to
enable dom.wakelock.enabled in about:config so that dbus messages were sent.)

## License

inhibit-bridge is available under the Simplified BSD License; see LICENSE for
the full text.
