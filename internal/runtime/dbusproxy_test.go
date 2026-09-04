package runtime

import "testing"

func TestUnixSocketAddressRejectsActiveTransports(t *testing.T) {
	for _, bad := range []string{
		"",
		"unixexec:path=/bin/sh,argv0=sh",      // starts a process
		"unixexec:path=/usr/bin/env",          // starts a process
		"tcp:host=127.0.0.1,port=1234",        // network transport
		"nonce-tcp:host=127.0.0.1,port=1",     // network transport
		"autolaunch:",                         // launches a bus
		"unix:path=/run/bus;unixexec:path=/x", // multi-address smuggling exec
		"unix:path=/run/bus,argv0=/bin/sh",    // unknown/dangerous key
		"unix:tmpdir=/tmp",                    // no concrete socket
	} {
		if path, ok := unixSocketAddress(bad); ok {
			t.Errorf("address %q must be rejected, got path %q", bad, path)
		}
	}
	for _, good := range []struct{ addr, canonical string }{
		{"unix:path=/run/user/1000/bus", "unix:path=/run/user/1000/bus"},
		{"unix:path=/run/user/1000/bus,guid=abc", "unix:path=/run/user/1000/bus,guid=abc"},
		{"unix:abstract=/tmp/dbus-abc", "unix:abstract=/tmp/dbus-abc"},
	} {
		if address, ok := unixSocketAddress(good.addr); !ok || address != good.canonical {
			t.Errorf("address %q: got (%q,%v), want (%q,true)", good.addr, address, ok, good.canonical)
		}
	}
}

func TestTrustedSessionBusAddressIgnoresInjectedExec(t *testing.T) {
	// Even if a unixexec address is present in the environment, the proxy's
	// upstream resolves to a plain unix socket, never the exec transport.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unixexec:path=/bin/sh,argv0=sh")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/4242")
	if got := trustedSessionBusAddress(); got != "unix:path=/run/user/4242/bus" {
		t.Errorf("unixexec address must be discarded, got %q", got)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/4242/custom-bus")
	if got := trustedSessionBusAddress(); got != "unix:path=/run/user/4242/custom-bus" {
		t.Errorf("valid unix address must be used, got %q", got)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:abstract=/tmp/dbus-session")
	if got := trustedSessionBusAddress(); got != "unix:abstract=/tmp/dbus-session" {
		t.Errorf("valid abstract unix address must be preserved, got %q", got)
	}
}

func TestDBusProxySocketIsUniquePerLaunch(t *testing.T) {
	first := newDBusProxySpec(newLaunchState("tool"))
	second := newDBusProxySpec(newLaunchState("tool"))
	if first.socketPath == second.socketPath {
		t.Fatalf("concurrent proxies share socket %q", first.socketPath)
	}
}
