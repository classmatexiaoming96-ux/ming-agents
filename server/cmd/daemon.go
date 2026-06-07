// Command daemon is a small process controller for the SHRIMP server: it can
// start the daemon detached (writing a pidfile + logfile), stop it via SIGTERM,
// report status, and tail its logs.
//
//	go run ./cmd start    # start detached (runs `go run .` from the module root)
//	go run ./cmd stop     # graceful stop (SIGTERM, waits up to 15s)
//	go run ./cmd status   # report running/stopped
//	go run ./cmd logs     # print the logfile
//
// Paths are overridable via SHRIMP_PIDFILE / SHRIMP_LOGFILE.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func pidfile() string {
	return ctlEnvOr("MING_AGENTS_PIDFILE", filepath.Join(os.TempDir(), "ming-agents-daemon.pid"))
}
func logfile() string {
	return ctlEnvOr("MING_AGENTS_LOGFILE", filepath.Join(os.TempDir(), "ming-agents-daemon.log"))
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "start":
		err = start()
	case "stop":
		err = stop()
	case "status":
		err = status()
	case "logs":
		err = logs()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: daemon [start|stop|status|logs]")
}

func start() error {
	if pid, ok := readPID(); ok && alive(pid) {
		return fmt.Errorf("already running (pid %d)", pid)
	}
	lf, err := os.OpenFile(logfile(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()

	// Run the daemon binary. cwd is the module root when invoked as
	// `go run ./cmd`, so "go run ." builds and runs the server package.
	cmd := exec.Command("go", "run", ".")
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.Env = os.Environ()
	// Detach into its own process group so it survives the controller exiting.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(pidfile(), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return err
	}
	// Release so the child keeps running after we exit.
	_ = cmd.Process.Release()
	fmt.Printf("started (pid %d), logs: %s\n", pid, logfile())
	return nil
}

func stop() error {
	pid, ok := readPID()
	if !ok {
		return errors.New("not running (no pidfile)")
	}
	if !alive(pid) {
		_ = os.Remove(pidfile())
		return errors.New("not running (stale pidfile removed)")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	// Wait up to 15s for graceful exit.
	for i := 0; i < 150; i++ {
		if !alive(pid) {
			_ = os.Remove(pidfile())
			fmt.Println("stopped")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon (pid %d) did not stop within 15s", pid)
}

func status() error {
	pid, ok := readPID()
	if !ok || !alive(pid) {
		fmt.Println("stopped")
		return nil
	}
	fmt.Printf("running (pid %d)\n", pid)
	return nil
}

func logs() error {
	f, err := os.Open(logfile())
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}

// --- helpers ---

func readPID() (int, bool) {
	b, err := os.ReadFile(pidfile())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Signal 0 checks existence without affecting the process.
	return syscall.Kill(pid, 0) == nil
}

func ctlEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
