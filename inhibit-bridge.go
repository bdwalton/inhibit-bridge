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

	"github.com/coreos/go-systemd/login1"
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
	//go:embed org.freedesktop.ScreenSaver.xml
	screensaverInterface string
	ssXML                = "<node>" + screensaverInterface + introspect.IntrospectDataString + "</node>"

	// CLI Flags
	heartbeatInterval = flag.Duration("heartbeat_interval", time.Duration(10*time.Second), "How long do we wait between active lock peer validations.")
	verbose           = flag.Bool("verbose", false, "If true, output logging status updates. Be quiet when false.")
	logfile           = flag.String("logfile", "", "If set, log to this path instead of the default (os.Stderr) target")
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
	prog      string
	dbusConn  *dbus.Conn
	loginConn *login1.Conn
	locks     map[uint]*lockDetails
	mtx       sync.Mutex
	doneCh    chan struct{}
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
		prog:      prog,
		dbusConn:  conn,
		loginConn: login,
		locks:     make(map[uint]*lockDetails),
		doneCh:    make(chan struct{}),
	}

	for _, p := range []dbus.ObjectPath{screensaverPath, legacyPath} {
		if err = ib.dbusConn.Export(ib, p, screensaver); err != nil {
			return nil, fmt.Errorf("couldn't export %q on %q: %v", screensaver, p, err)
		}
		if err = ib.dbusConn.Export(introspect.Introspectable(ssXML), p, intro); err != nil {
			return nil, fmt.Errorf("couldn't export %q on %q: %v", intro, p, err)
		}
	}

	go ib.heartbeatCheck()

	return ib, nil
}

func (i *inhibitBridge) heartbeatCheck() {
	ticker := time.NewTicker(*heartbeatInterval)

	maybeLog("Heartbeat checker started.\n")

	for {
		select {
		case <-ticker.C:
			maybeLog("Heartbeck checker running.\n")
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
			i.mtx.Unlock()
		case <-i.doneCh:
			maybeLog("Heartbeat checker stopping.\n")
			close(i.doneCh)
			return
		}
	}
}

func (i *inhibitBridge) shutdown() {
	i.dbusConn.Close()

	// Shutdown the heartbeatCheck and wait for it
	i.doneCh <- struct{}{}
	<-i.doneCh

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

	sigLog := make(chan os.Signal, 1)
	signal.Notify(sigLog, syscall.SIGUSR1)

	for {
		select {
		case s := <-sigQuit:
			maybeLog("Received signal %q. Shutting down...\n", s)
			ib.shutdown()
			maybeLog("Goodbye.\n")
			os.Exit(0)
		case <-sigLog:
			*verbose = !*verbose
			reallyLog("Toggling log output. Now %t.", *verbose)
		}
	}
}
