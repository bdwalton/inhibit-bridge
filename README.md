# inhibit-bridge

This is a little utility to bridge between dbus org.freedesktop.ScreenSaver
Inhibit and UnInhibit messages to systemd's logind idle inhibits. The usecase is
for environments like i3/sway that aren't full desktop environments with all of
this kind of plumbing by default to have a facility to inhibit (eg) swayidle
from locking the screen when browsers are playing video.

Right now, it is entirely bare bones. It has no notion of heart beat on the
inhbits and relies on the upstream requestor not crashing before UnInhibit is
called. We may add these features later, but for now they're low priority.

I've only tested this with Chrome as it's the only browser I use. If it doesn't
work with others, I'll fix that or accept patches.

## License

inhibit-bridge is available under the Simplified BSD License; see LICENSE for
the full text.
