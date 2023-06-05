package main

import (
	_ "embed"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

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

type inhibitBridge struct {
	dbusConn *dbus.Conn
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

	ib := &inhibitBridge{dbusConn: conn}

	conn.Export(ib, "/org/freedesktop/ScreenSaver", "org.freedesktop.ScreenSaver")
	conn.Export(introspect.Introspectable(introXML), "/com/github/guelfey/Demo",
		"org.freedesktop.DBus.Introspectable")

	return ib, nil
}

func (i *inhibitBridge) Shutdown() {
	i.dbusConn.Close()
}

func (i *inhibitBridge) Inhibit(who, why string) (uint, *dbus.Error) {
	cookie := rand.Uint32()

	fmt.Printf("%s: who: %q; why: %q; cookie: %d\n", time.Now().Format(time.RFC3339), who, why, cookie)
	return uint(cookie), nil
}

func (i *inhibitBridge) UnInhibit(cookie uint32) *dbus.Error {
	fmt.Printf("%s: cookie: %d\n", time.Now().Format(time.RFC3339), cookie)
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
