package main

import (
	_ "embed"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/login1"
	"github.com/godbus/dbus/introspect"
	"github.com/godbus/dbus/v5"
)

const (
	screensaver = "org.freedesktop.ScreenSaver"
)

const dtd = `<!DOCTYPE node PUBLIC "-//freedesktop//DTD D-BUS Object Introspection 1.0//EN"
"http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd">`

//go:embed org.freedesktop.ScreenSaver.xml
var screensaverInterface string

var ssXML = dtd + "<node>" + screensaverInterface + introspect.IntrospectDataString + "</node>"
var introXML = dtd + "<node>" + introspect.IntrospectDataString + "</node>"

type lockDetails struct {
	cookie   uint
	ts       time.Time
	who, why string
	fd       *os.File
}

func (ld *lockDetails) String() string {
	return fmt.Sprintf("%s: %q / %q (%d)", ld.ts.Format(time.RFC3339), ld.who, ld.why, ld.cookie)
}

type inhibitBridge struct {
	dbusConn  *dbus.Conn
	loginConn *login1.Conn
	locks     map[uint]*lockDetails
	mtx       sync.Mutex
}

func NewInhibitBridge() (*inhibitBridge, error) {
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
		dbusConn:  conn,
		loginConn: login,
		locks:     make(map[uint]*lockDetails),
	}

	conn.Export(ib, "/org/freedesktop/ScreenSaver", "org.freedesktop.ScreenSaver")
	conn.Export(introspect.Introspectable(introXML), "/com/github/guelfey/Demo",
		"org.freedesktop.DBus.Introspectable")

	return ib, nil
}

func (i *inhibitBridge) Shutdown() {
	i.dbusConn.Close()
	i.loginConn.Close()
}

func (i *inhibitBridge) Inhibit(who, why string) (uint, *dbus.Error) {
	fd, err := i.loginConn.Inhibit("idle", "idle-bridge", who+" "+why, "block")
	if err != nil {
		return 0, dbus.MakeFailedError(err)
	}

	ld := &lockDetails{
		cookie: uint(rand.Uint32()),
		ts:     time.Now(),
		who:    who,
		why:    why,
		fd:     fd,
	}

	i.mtx.Lock()
	defer i.mtx.Unlock()
	i.locks[ld.cookie] = ld

	fmt.Printf("Inhibit: %s\n", ld)
	return ld.cookie, nil
}

func (i *inhibitBridge) UnInhibit(cookie uint32) *dbus.Error {
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

	fmt.Printf("UnInhibit: %s\n", ld)
	return nil
}

func main() {
	ib, err := NewInhibitBridge()
	if err != nil {
		log.Fatalf("Setup failure: %v", err)
		os.Exit(1)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("idle-bridge: Received signal %q. Shutting down...", <-sig)
	ib.Shutdown()
}
