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
	for _, good := range []struct{ addr, path string }{
		{"unix:path=/run/user/1000/bus", "/run/user/1000/bus"},
		{"unix:path=/run/user/1000/bus,guid=abc", "/run/user/1000/bus"},
	} {
		if path, ok := unixSocketAddress(good.addr); !ok || path != good.path {
			t.Errorf("address %q: got (%q,%v), want (%q,true)", good.addr, path, ok, good.path)
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
}
