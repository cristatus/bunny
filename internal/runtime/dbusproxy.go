package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
)

// dbusProxySpec is the filtered portal bus a hardened policy with
// features.dbus: true selects. The sandbox sees only the proxy socket and
// cannot call the user service manager; hardened mode never binds the raw
// user or system bus, and never falls back to it.
type dbusProxySpec struct {
	socketPath string // host-side socket the proxy listens on
	busAddress string // real session bus the proxy connects to
}

// FindXDGDBusProxy locates the filter proxy. A hardened policy requesting
// D-Bus fails with this install hint when it is absent.
func FindXDGDBusProxy() (string, error) {
	return findTool("xdg-dbus-proxy", "required for hardened D-Bus", "xdg-dbus-proxy", "xdg-dbus-proxy")
}

// newDBusProxySpec only computes paths; the proxy binary lookup and the
// runtime directory happen at exec time, keeping planning (and --explain)
// free of side effects. The upstream address is taken from trusted host
// state, never the package's merged environment: xdg-dbus-proxy connects to
// it on the host, outside any sandbox, and a D-Bus address can name an active
// transport (unixexec:path=/bin/sh,...) that starts a process. So the address
// is derived from Bunny's own environment and validated to a plain unix:
// socket; a package cannot redirect the proxy to an exec transport.
func newDBusProxySpec(launch launchState) *dbusProxySpec {
	return &dbusProxySpec{
		socketPath: launch.path("dbus.sock"),
		busAddress: trustedSessionBusAddress(),
	}
}

// trustedSessionBusAddress returns the host session bus as a plain unix:
// socket address. It reads Bunny's own environment (not the package's merged
// env) and falls back to the conventional runtime socket; an address naming
// any other transport, extra keys, or multiple addresses is discarded.
func trustedSessionBusAddress() string {
	if address, ok := unixSocketAddress(os.Getenv("DBUS_SESSION_BUS_ADDRESS")); ok {
		return address
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return "unix:path=" + filepath.Join(rt, "bus")
	}
	return fmt.Sprintf("unix:path=/run/user/%d/bus", os.Getuid())
}

// unixSocketAddress accepts only a single unix: transport addressing a
// filesystem or abstract socket — the two forms that merely connect. Every
// active transport (unixexec:, autolaunch:, tcp:, nonce-tcp:) and any unknown
// key is rejected, so nothing a package could inject makes the proxy run a
// program or open a network connection.
func unixSocketAddress(addr string) (string, bool) {
	if addr == "" || strings.ContainsRune(addr, ';') {
		return "", false // no multi-address strings
	}
	transport, rest, ok := strings.Cut(addr, ":")
	if !ok || transport != "unix" {
		return "", false
	}
	var endpoint string
	seenGUID := false
	for _, kv := range strings.Split(rest, ",") {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			return "", false
		}
		switch key {
		case "path", "abstract":
			if endpoint != "" || value == "" {
				return "", false
			}
			endpoint = key
		case "guid":
			if seenGUID || value == "" {
				return "", false
			}
			seenGUID = true
		default:
			return "", false
		}
	}
	if endpoint == "" {
		return "", false
	}
	return addr, true
}

// proxyArgs is the portal-only preset: the sandbox may call the portal
// services and receive their broadcasts, nothing else. Custom name and method
// grants are deferred until a real catalog package needs them, because broad
// talk access can recreate the session-bus escape.
func (s *dbusProxySpec) proxyArgs() []string {
	return []string{
		s.busAddress, s.socketPath,
		"--filter",
		"--call=org.freedesktop.portal.*=*",
		"--broadcast=org.freedesktop.portal.*=@/org/freedesktop/portal/*",
		"--fd=3",
	}
}

// runSupervised runs the sandbox under a small supervisor instead of the
// normal syscall.Exec path: the proxy must remain outside the sandbox to
// reach the real bus. The supervisor starts the proxy, waits for its
// readiness byte, launches the sandbox (argv[0] is the bwrap path), forwards
// signals, and terminates the proxy on every exit path, exiting with the
// sandbox's status.
func runSupervised(spec *dbusProxySpec, argv, env []string) error {
	proxyPath, err := FindXDGDBusProxy()
	if err != nil {
		return err
	}
	if _, err := ensureRuntimeStateDir(); err != nil {
		return err
	}
	launchDir := launchState{dir: filepath.Dir(spec.socketPath)}
	if err := launchDir.ensure(); err != nil {
		return err
	}
	defer launchDir.cleanup()
	_ = os.Remove(spec.socketPath) // a stale socket from a crashed run

	ready, readyWrite, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create proxy readiness pipe: %w", err)
	}
	proxy := exec.Command(proxyPath, spec.proxyArgs()...)
	proxy.Env = env                           // trusted, loader-sanitized, like the sandbox child below
	proxy.ExtraFiles = []*os.File{readyWrite} // becomes fd 3 in the proxy
	proxy.Stderr = os.Stderr
	if err := proxy.Start(); err != nil {
		return fmt.Errorf("start xdg-dbus-proxy: %w", err)
	}
	readyWrite.Close()
	stopProxy := func() {
		// Closing our end of the readiness pipe asks the proxy to exit (it
		// watches that descriptor); the kill is a fallback for a wedged one.
		ready.Close()
		_ = proxy.Process.Kill()
		_ = proxy.Wait()
		_ = os.Remove(spec.socketPath)
	}

	// The proxy writes one byte when its socket is listening. The pipe must
	// then STAY open: the proxy treats its closure as the exit signal and
	// removes the socket, which would race the sandbox start.
	if err := ready.SetReadDeadline(time.Now().Add(10 * time.Second)); err == nil {
		buf := make([]byte, 1)
		if _, err := ready.Read(buf); err != nil {
			stopProxy()
			return fmt.Errorf("xdg-dbus-proxy did not become ready: %w", err)
		}
		_ = ready.SetReadDeadline(time.Time{})
	}

	child := exec.Command(argv[0], argv[1:]...)
	child.Env = env
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		stopProxy()
		return fmt.Errorf("start sandbox: %w", err)
	}

	signals := make(chan os.Signal, 16)
	signal.Notify(signals)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-signals:
				if sig == syscall.SIGCHLD {
					continue
				}
				_ = child.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()

	waitErr := child.Wait()
	close(done)
	signal.Stop(signals)
	stopProxy()

	status := 0
	if waitErr != nil {
		exitErr, ok := waitErr.(*exec.ExitError)
		if !ok {
			return fmt.Errorf("sandbox wait: %w", waitErr)
		}
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			status = 128 + int(ws.Signal())
		} else {
			status = exitErr.ExitCode()
		}
	}
	log.Debug("Supervised sandbox exit", "status", status)
	os.Exit(status)
	return nil // unreachable
}
