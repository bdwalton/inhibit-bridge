package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"fyne.io/systray"
	"github.com/coreos/go-systemd/login1"
	"github.com/esiqveland/notify"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	listNames       = "org.freedesktop.DBus.ListNames"
	intro           = "org.freedesktop.DBus.Introspectable"
	screensaver     = "org.freedesktop.ScreenSaver"
	screensaverPath = "/org/freedesktop/ScreenSaver"
	// Firefox looks for this path, not /org/freedesktop/ScreenSaver
	legacyPath = "/ScreenSaver"
)

var (
	//go:embed icons/uninhibited.png
	iconUninhibited []byte
	//go:embed icons/auto-inhibited.png
	iconAutoInhibited []byte
	//go:embed icons/manually-inhibited.png
	iconManuallyInhibited []byte

	//go:embed org.freedesktop.ScreenSaver.xml
	screensaverInterface string
	ssXML                = "<node>" + screensaverInterface + introspect.IntrospectDataString + "</node>"

	// CLI Flags
	heartbeatInterval = flag.Duration("heartbeat_interval", time.Duration(10*time.Second), "How long do we wait between active lock peer validations.")
	logfile           = flag.String("logfile", "", "If set, log to this path instead of the default (os.Stderr) target")
	manualTimeout     = flag.Duration("manual_inhibit_timeout", 60*time.Minute, "The maximum time to allow a manual inhibit to persist. 0m disables this feature.")
	sendNotifications = flag.Bool("notify", true, "If true, send notifications on interesting state changes.")
	verbose           = flag.Bool("verbose", false, "If true, output logging status updates. Be quiet when false.")
)

func maybeLog(fmt string, args ...interface{}) {
	if *verbose {
		reallyLog(fmt, args...)
	}
}

func reallyLog(fmt string, args ...interface{}) {
	log.Printf(fmt, args...)
}

// lockDetails represents all of the state for an individual inhibit
// lock that we've requested from systemd.
type lockDetails struct {
	cookie   uint
	peer     dbus.Sender
	who, why string
	fd       *os.File
}

// String returns a useful textual representation of a lock.
func (ld *lockDetails) String() string {
	return fmt.Sprintf("%q / %q (%q, %d)", ld.who, ld.why, ld.peer, ld.cookie)
}

// inhibitBridge represents the state required to bridge dbus inhibit
// requests to systemd logind idle inhibits.
type inhibitBridge struct {
	prog            string
	dbusConn        *dbus.Conn
	loginConn       *login1.Conn
	manualInhibit   *systray.MenuItem
	localCookie     uint
	locks           map[uint]*lockDetails
	mtx             sync.Mutex
	trayCh, doneCh  chan struct{}
	manualTimeoutCh chan struct{}
}

func NewInhibitBridge(prog string) (*inhibitBridge, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("session bus connect failed: %v", err)
	}

	r, err := conn.RequestName(screensaver, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("conn.RequestName(%q, 0): %v:", screensaver, err)
	}
	if r != dbus.RequestNameReplyPrimaryOwner {
		return nil, fmt.Errorf("conn.RequestName(%q, 0): not the primary owner.", screensaver)
	}

	login, err := login1.New()
	if err != nil {
		return nil, fmt.Errorf("login1.New() failed: %v", err)
	}

	ib := &inhibitBridge{
		prog:            prog,
		dbusConn:        conn,
		loginConn:       login,
		locks:           make(map[uint]*lockDetails),
		trayCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
		manualTimeoutCh: make(chan struct{}),
	}

	for _, p := range []dbus.ObjectPath{screensaverPath, legacyPath} {
		if err = ib.dbusConn.Export(ib, p, screensaver); err != nil {
			return nil, fmt.Errorf("couldn't export %q on %q: %v", screensaver, p, err)
		}
		if err = ib.dbusConn.Export(introspect.Introspectable(ssXML), p, intro); err != nil {
			return nil, fmt.Errorf("couldn't export %q on %q: %v", intro, p, err)
		}
	}

	systray.SetTitle(prog)
	systray.SetTemplateIcon(iconUninhibited, iconUninhibited)

	ib.setStatus()

	// We don't need any cleanup when this gets shut down, so ignore the end func().
	sysStart, _ := systray.RunWithExternalLoop(ib.systrayStart, func() {})

	go sysStart()
	go ib.heartbeatCheck()

	return ib, nil
}

func (i *inhibitBridge) setStatus() {
	if i.localCookie > 0 {
		systray.SetIcon(iconManuallyInhibited)
	} else if len(i.locks) > 0 {
		systray.SetIcon(iconAutoInhibited)
	} else {
		systray.SetIcon(iconUninhibited)
	}

	systray.SetTitle(fmt.Sprintf("%s: %d inhibits (manual: %t)", i.prog, len(i.locks), i.localCookie > 0))
}

func (i *inhibitBridge) dbusName() dbus.Sender {
	return dbus.Sender(i.dbusConn.Names()[0])
}

func (i *inhibitBridge) manualInhibitToggle() {
	// Potential race here at startup, but shouldn't be
	// detrimental.
	if i.manualInhibit != nil {
		i.manualInhibit.ClickedCh <- struct{}{}
	}
}

func (i *inhibitBridge) manualInhibitTimeout(d time.Duration, cancelCh <-chan struct{}) {
	select {
	case <-cancelCh:
		maybeLog("Manual inhibit timeout was cancelled.\n")
	case <-time.NewTimer(d).C:
		maybeLog("Manual inhibit timeout reached.\n")
		i.manualTimeoutCh <- struct{}{}
	}
}

func (i *inhibitBridge) manualUninhibit() {
	if i.localCookie != 0 {
		if err := i.UnInhibit(i.dbusName(), uint32(i.localCookie)); err != nil {
			maybeLog("Error manually unihibiting after timeout: %v\n", err)
			return
		}

		i.localCookie = 0
		i.manualInhibit.Uncheck()
	}
}
func (i *inhibitBridge) systrayStart() {
	var notificationID uint32
	cancelCh := make(chan struct{})

	i.manualInhibit = systray.AddMenuItemCheckbox("Manually inhibit screen lock", "", false)

	for {
		select {
		case <-i.trayCh:
			maybeLog("Exiting systray.")
			close(i.manualTimeoutCh)
			close(i.trayCh)
			return
		case <-i.manualTimeoutCh:
			i.manualUninhibit()
			i.notifyInhibitChange("Released manual inhibit after timeout.", 0)
		case <-i.manualInhibit.ClickedCh:
			if i.manualInhibit.Checked() {
				i.manualUninhibit()

				notificationID = i.notifyInhibitChange("Manual screen lock inhibit cleared", notificationID)
				if *manualTimeout > 0 {
					// Cancel the timeout on manual the inhibit
					cancelCh <- struct{}{}
				}

			} else {
				cookie, err := i.Inhibit(i.dbusName(), "systray", "clicked")
				if err != nil {
					maybeLog("Error manually inhibiting: %v\n", err)
					continue
				}

				i.localCookie = cookie
				i.manualInhibit.Check()

				m := fmt.Sprintf("Manual screen lock inhibit placed.")
				if *manualTimeout > 0 {
					m += fmt.Sprintf(" It will expire in %s", *manualTimeout)
				}
				notificationID = i.notifyInhibitChange(m, notificationID)
				if *manualTimeout > 0 {
					go i.manualInhibitTimeout(*manualTimeout, cancelCh)
				}
			}
		}
		i.mtx.Lock()
		i.setStatus()
		i.mtx.Unlock()
	}
}

func (i *inhibitBridge) notifyInhibitChange(message string, replaces uint32) uint32 {
	if !*sendNotifications {
		return 0
	}

	n := notify.Notification{
		AppName:       i.prog,
		ReplacesID:    replaces,
		Summary:       i.prog,
		Body:          message,
		ExpireTimeout: 5 * time.Second,
	}

	id, err := notify.SendNotification(i.dbusConn, n)
	if err != nil {
		maybeLog("Error sending notification: %v\n", err)
	}

	return id
}

func (i *inhibitBridge) heartbeatCheck() {
	ticker := time.NewTicker(*heartbeatInterval)

	maybeLog("Heartbeat checker started.\n")

	for {
		select {
		case <-ticker.C:
			maybeLog("Heartbeat checker running.\n")
			// Not every peer implements the
			// org.freedesktop.DBus.Peer interface, so
			// we'll simply lookup every active peer on
			// the bus. Using that, we can determine if a
			// peer that requested the inhibit is still
			// alive.
			var activeNames []dbus.Sender
			if err := i.dbusConn.BusObject().Call(listNames, 0).Store(&activeNames); err != nil {
				maybeLog("Error calling %q: %v\n", listNames, err)
				continue
			}

			nameMap := make(map[dbus.Sender]struct{})
			for _, n := range activeNames {
				nameMap[n] = struct{}{}
			}

			i.mtx.Lock()
			for _, ld := range i.locks {
				maybeLog("Heartbeat checking: %s\n", ld)
				if _, ok := nameMap[ld.peer]; !ok {
					maybeLog("Missing peer %q; Dropping: %s\n", ld.peer, ld)
					ld.fd.Close()
					delete(i.locks, ld.cookie)
				}
			}
			i.setStatus()
			i.mtx.Unlock()
		case <-i.doneCh:
			maybeLog("Heartbeat checker stopping.\n")
			close(i.doneCh)
			return
		}
	}
}

func (i *inhibitBridge) shutdown() {
	// Stop programatic inhibits
	i.dbusConn.Close()

	// Stop manual inhibits
	i.trayCh <- struct{}{}
	<-i.trayCh

	// With all inhibit sources stopped, we can shut down the heartbeat.
	i.doneCh <- struct{}{}
	<-i.doneCh

	// Close any open files to release all inhibits.
	i.mtx.Lock()
	for _, ld := range i.locks {
		if err := ld.fd.Close(); err != nil {
			maybeLog("Error closing lock for %q: %v\n", ld, err)
		}
	}
	i.mtx.Unlock()

	i.loginConn.Close()
}

func (i *inhibitBridge) Inhibit(from dbus.Sender, who, why string) (uint, *dbus.Error) {
	fd, err := i.loginConn.Inhibit("idle", i.prog, who+" "+why, "block")
	if err != nil {
		return 0, dbus.MakeFailedError(err)
	}

	ld := &lockDetails{
		cookie: uint(rand.Uint32()),
		peer:   from,
		who:    who,
		why:    why,
		fd:     fd,
	}

	i.mtx.Lock()
	defer i.mtx.Unlock()
	i.locks[ld.cookie] = ld

	maybeLog("Inhibit: %s\n", ld)
	i.setStatus()

	return ld.cookie, nil
}

func (i *inhibitBridge) UnInhibit(from dbus.Sender, cookie uint32) *dbus.Error {
	i.mtx.Lock()
	defer i.mtx.Unlock()

	ld, ok := i.locks[uint(cookie)]
	if !ok {
		return dbus.MakeFailedError(fmt.Errorf("%d is an invalid cookie", cookie))
	}

	if from != ld.peer {
		return dbus.MakeFailedError(fmt.Errorf("%q is not the originating peer for cookie %d", from, cookie))
	}

	delete(i.locks, ld.cookie)

	if err := ld.fd.Close(); err != nil {
		return dbus.MakeFailedError(fmt.Errorf("failed to close clock for cookie %d -> %s", cookie, ld.fd.Name()))
	}

	maybeLog("UnInhibit: %s\n", ld)
	i.setStatus()

	return nil
}

func main() {
	flag.Parse()

	if *logfile != "" {
		lf, err := os.OpenFile(*logfile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			log.Fatalf("Couldn't open logfile %q: %v\n", *logfile, err)
		}
		log.SetOutput(lf)
	}

	prog, err := os.Executable()
	if err != nil {
		maybeLog("Error determining program executable: %v\n", err)
		os.Exit(1)
	}
	base := filepath.Base(prog)
	ib, err := NewInhibitBridge(base)
	if err != nil {
		maybeLog("Setup failure: %v\n", err)
		os.Exit(1)
	}
	log.SetPrefix(base + ": ")
	maybeLog("Running.\n")

	sigQuit := make(chan os.Signal, 1)
	signal.Notify(sigQuit, syscall.SIGINT, syscall.SIGTERM)

	sigToggle := make(chan os.Signal, 1)
	signal.Notify(sigToggle, syscall.SIGUSR1)

	for {
		select {
		case s := <-sigQuit:
			maybeLog("Received signal %q. Shutting down...\n", s)
			ib.shutdown()
			maybeLog("Goodbye.\n")
			os.Exit(0)
		case <-sigToggle:
			maybeLog("Received SIGUSR1. Toggling inhibit.\n")
			ib.manualInhibitToggle()
		}
	}
}
