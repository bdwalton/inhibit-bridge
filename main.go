package main

import (
	_ "embed"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/coreos/go-systemd/login1"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
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
)

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
	return fmt.Sprintf("%q / %q (%d)", ld.who, ld.why, ld.cookie)
}

// inhibitBridge represents the state required to bridge dbus inhibit
// requests to systemd logind idle inhibits.
type inhibitBridge struct {
	prog      string
	dbusConn  *dbus.Conn
	loginConn *login1.Conn
	locks     map[uint]*lockDetails
	mtx       sync.Mutex
}

func NewInhibitBridge(prog string) (*inhibitBridge, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to connect to session bus:", err)
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
	}

	for _, p := range []dbus.ObjectPath{screensaverPath, legacyPath} {
		if err = ib.dbusConn.Export(ib, p, screensaver); err != nil {
			return nil, fmt.Errorf("couldn't export %q on %q: %v", screensaver, p, err)
		}
		if err = ib.dbusConn.Export(introspect.Introspectable(ssXML), p, intro); err != nil {
			return nil, fmt.Errorf("couldn't export %q on %q: %v", intro, p, err)
		}
	}

	return ib, nil
}

func (i *inhibitBridge) Shutdown() {
	i.dbusConn.Close()
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

	log.Printf("Inhibit: %s\n", ld)
	return ld.cookie, nil
}

func (i *inhibitBridge) UnInhibit(from dbus.Sender, cookie uint32) *dbus.Error {
	i.mtx.Lock()
	defer i.mtx.Unlock()

	ld, ok := i.locks[uint(cookie)]
	if !ok {
		return dbus.MakeFailedError(fmt.Errorf("%d is an invalid cookie", cookie))
	}
	delete(i.locks, ld.cookie)

	if err := ld.fd.Close(); err != nil {
		return dbus.MakeFailedError(fmt.Errorf("failed to close clock for cookie %d -> %s", cookie, ld.fd.Name()))
	}

	log.Printf("UnInhibit: %s\n", ld)
	return nil
}

func main() {
	prog, err := os.Executable()
	if err != nil {
		log.Fatalf("Error determining program executable: %v\n", err)
		os.Exit(1)
	}
	ib, err := NewInhibitBridge(filepath.Base(prog))
	if err != nil {
		log.Fatalf("Setup failure: %v\n", err)
		os.Exit(1)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("%s: Received signal %q. Shutting down...\n", prog, <-sig)
	ib.Shutdown()
}
