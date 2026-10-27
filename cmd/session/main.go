// Kzure's unprivileged terminal supervisor. This binary runs inside a workspace,
// never in the host namespaces. A private socket controls only this session.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type control struct {
	Action string `json:"action"`
	Rows   uint16 `json:"rows"`
	Cols   uint16 `json:"cols"`
}

func main() {
	if len(os.Args) < 3 || len(os.Args[2]) != 32 || strings.Trim(os.Args[2], "0123456789abcdef") != "" {
		os.Exit(2)
	}
	sid := os.Args[2]
	// The directory is private to the workspace user; every session has a random ID.
	dir := "/tmp/kzure-terminal-1000"
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		fail(err)
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		fail(fmt.Errorf("unsafe terminal directory"))
	}
	sock := filepath.Join(dir, sid+".sock")
	if os.Args[1] != "run" {
		c, err := net.DialTimeout("unix", sock, time.Second)
		if err != nil {
			os.Exit(1)
		}
		defer c.Close()
		var m control
		if len(os.Args) != 4 {
			os.Exit(2)
		}
		if err = json.Unmarshal([]byte(os.Args[3]), &m); err != nil {
			fail(err)
		}
		c.SetDeadline(time.Now().Add(2 * time.Second))
		if err = json.NewEncoder(c).Encode(m); err != nil {
			fail(err)
		}
		// Wait for acknowledgment so Docker exec completion means the request was handled.
		var ack bool
		if err = json.NewDecoder(c).Decode(&ack); err != nil || !ack {
			os.Exit(1)
		}
		return
	}
	shell := "/bin/sh"
	if _, err := os.Stat("/bin/bash"); err == nil {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "-i")
	cmd.Dir = "/data"
	home := "/data"
	if info, e := os.Stat("/data/home/kzure"); e == nil && info.IsDir() {
		home = "/data/home/kzure"
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "HOME="+home, "KZURE_TERMINAL_SESSION="+sid)
	term, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		fail(err)
	}
	defer term.Close()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		killSession(cmd.Process.Pid)
		fail(err)
	}
	defer ln.Close()
	defer os.Remove(sock)
	var once sync.Once
	stop := func() { once.Do(func() { killSession(cmd.Process.Pid) }) }
	defer stop()
	var lastControl atomic.Int64
	lastControl.Store(time.Now().UnixNano())
	// Docker may retain an exec's stdin after a crashed attach client. A local
	// lease ends the child session even when no cleanup request can arrive.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if time.Since(time.Unix(0, lastControl.Load())) > 25*time.Second {
				stop()
				return
			}
		}
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	go func() { <-signals; stop() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.SetDeadline(time.Now().Add(2 * time.Second))
			var m control
			if json.NewDecoder(c).Decode(&m) == nil {
				lastControl.Store(time.Now().UnixNano())
				switch m.Action {
				case "resize":
					if m.Rows >= 2 && m.Rows <= 500 && m.Cols >= 2 && m.Cols <= 500 {
						_ = pty.Setsize(term, &pty.Winsize{Rows: m.Rows, Cols: m.Cols})
					}
				case "stop":
					stop()
				}
				_ = json.NewEncoder(c).Encode(true)
			}
			c.Close()
		}
	}()
	go func() { _, _ = io.Copy(term, os.Stdin); stop() }()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(os.Stdout, term); close(done) }()
	err = cmd.Wait()
	stop()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
	}
	if e, ok := err.(*exec.ExitError); ok {
		_ = ln.Close()
		_ = os.Remove(sock)
		os.Exit(e.ExitCode())
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, "terminal:", err); os.Exit(1) }

// The PTY child is a new Unix session. Clean up only processes in that session,
// including foreground job groups, never another shell or the container's PID 1.
func killSession(sid int) {
	kill := func(sig syscall.Signal) {
		entries, _ := os.ReadDir("/proc")
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil || pid <= 1 {
				continue
			}
			b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
			if err != nil {
				continue
			}
			i := strings.LastIndexByte(string(b), ')')
			if i < 0 {
				continue
			}
			fields := strings.Fields(string(b[i+1:]))
			if len(fields) < 4 {
				continue
			}
			session, _ := strconv.Atoi(fields[3])
			if session == sid {
				_ = syscall.Kill(pid, sig)
			}
		}
	}
	kill(syscall.SIGHUP)
	time.Sleep(150 * time.Millisecond)
	kill(syscall.SIGKILL)
}
