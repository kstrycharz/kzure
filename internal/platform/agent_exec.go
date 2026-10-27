package platform

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const sessionBinary = "/tmp/.kzure-terminal-supervisor-v1"

func (d *dockerClient) createExec(ctx context.Context, cid string, command []string) (string, error) {
	b, e := d.call(ctx, "POST", "/containers/"+cid+"/exec", map[string]any{"AttachStdin": true, "AttachStdout": true, "AttachStderr": true, "Tty": false, "User": "1000:1000", "WorkingDir": "/data", "Cmd": command, "Env": []string{"TERM=xterm-256color"}})
	if e != nil {
		return "", e
	}
	var out struct {
		ID string `json:"Id"`
	}
	e = json.Unmarshal(b, &out)
	return out.ID, e
}

func (d *dockerClient) execStream(ctx context.Context, eid string) (net.Conn, *bufio.Reader, error) {
	conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", env("DOCKER_SOCKET", "/var/run/docker.sock"))
	if e != nil {
		return nil, nil, e
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	payload := []byte(`{"Detach":false,"Tty":false}`)
	req, _ := http.NewRequest("POST", "http://docker/v1.47/exec/"+eid+"/start", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	if e = req.Write(conn); e != nil {
		conn.Close()
		return nil, nil, e
	}
	reader := bufio.NewReader(conn)
	response, e := http.ReadResponse(reader, req)
	if e != nil {
		conn.Close()
		return nil, nil, e
	}
	if response.StatusCode != 101 && response.StatusCode != 200 {
		conn.Close()
		return nil, nil, fmt.Errorf("Docker exec start: %d", response.StatusCode)
	}
	conn.SetDeadline(time.Time{})
	return conn, reader, nil
}

func (d *dockerClient) controlExec(cid, sid string, frame terminalFrame) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	action := "resize"
	if frame.Type == "stop" {
		action = "stop"
	}
	if frame.Type == "heartbeat" {
		action = "heartbeat"
	}
	payload, _ := json.Marshal(map[string]any{"action": action, "rows": frame.Rows, "cols": frame.Cols})
	eid, e := d.createExec(ctx, cid, []string{sessionBinary, "control", sid, string(payload)})
	if e != nil {
		return e
	}
	_, e = d.call(ctx, "POST", "/exec/"+eid+"/start", map[string]bool{"Detach": false, "Tty": false})
	return e
}

func (d *dockerClient) serveTerminal(w http.ResponseWriter, r *http.Request) {
	rid, sid, cid := r.PathValue("id"), r.URL.Query().Get("session"), r.URL.Query().Get("container")
	if len(rid) != 32 || len(sid) != 32 || strings.Trim(rid+sid, "0123456789abcdef") != "" {
		jsonOut(w, 400, map[string]string{"error": "Invalid terminal identity"})
		return
	}
	in, e := d.inspect(r.Context(), rid)
	if e != nil || !in.State.Running || in.ID != cid || !strings.HasPrefix(in.Config.Labels["io.kzure.resource"], rid) {
		jsonOut(w, 409, map[string]string{"error": "Workspace is not ready"})
		return
	}
	// Enforce provider identity independently of caller-supplied IDs.
	var config struct {
		Config struct {
			User, WorkingDir string
			Image            string
		}
	}
	raw, e := d.call(r.Context(), "GET", "/containers/"+cid+"/json", nil)
	if e != nil || json.Unmarshal(raw, &config) != nil || config.Config.WorkingDir != "/data" {
		jsonOut(w, 403, map[string]string{"error": "Interactive exec requires an unprivileged workspace"})
		return
	}
	approved := false
	for _, item := range Catalog {
		if strings.HasPrefix(item.ID, "workspace-") && d.imageLocks[item.Image] == config.Config.Image {
			approved = true
			break
		}
	}
	// Operator-owned image locks retain earlier approved workspace revisions so
	// rebuilding the catalog does not invalidate existing workspace terminals.
	if !approved {
		for ref, identity := range d.imageLocks {
			if strings.HasPrefix(ref, "history/kzure/workspace-") && identity == config.Config.Image {
				approved = true
				break
			}
		}
	}
	if !approved {
		jsonOut(w, 403, map[string]string{"error": "Terminal requires an approved workspace image"})
		return
	}
	if config.Config.User != "1000:1000" && config.Config.User != "1000" && !isSSHWorkspace(in.Config.Labels["io.kzure.type"]) {
		jsonOut(w, 403, map[string]string{"error": "Terminal requires an approved workspace identity"})
		return
	}
	// The immutable supervisor is supplied by the platform image, not by a tenant.
	helper, e := os.ReadFile("/usr/local/bin/kzure-session")
	if e == nil {
		e = d.archive(r.Context(), cid, "/tmp", strings.TrimPrefix(sessionBinary, "/tmp/"), helper, 0555)
	}
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": "Could not prepare terminal supervisor"})
		return
	}
	eid, e := d.createExec(r.Context(), cid, []string{sessionBinary, "run", sid})
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": "Could not create terminal"})
		return
	}
	conn, reader, e := d.execStream(r.Context(), eid)
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": "Could not start terminal"})
		return
	}
	defer conn.Close()
	defer func() { _ = d.controlExec(cid, sid, terminalFrame{Type: "stop"}) }()
	ws, e := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}).Upgrade(w, r, nil)
	if e != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(65536)
	_ = ws.UnderlyingConn().SetDeadline(time.Time{})
	var writer sync.Mutex
	send := func(frame terminalFrame) error {
		writer.Lock()
		defer writer.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return ws.WriteJSON(frame)
	}
	done := make(chan error, 2)
	outputAck := make(chan struct{}, 1)
	go func() {
		for {
			var frame terminalFrame
			if err := ws.ReadJSON(&frame); err != nil {
				done <- err
				return
			}
			switch frame.Type {
			case "ack":
				select {
				case outputAck <- struct{}{}:
				default:
				}
			case "input":
				if len(frame.Data) > 32768 {
					done <- errors.New("Input exceeds limit")
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if _, err := io.WriteString(conn, frame.Data); err != nil {
					done <- err
					return
				}
			case "resize":
				if validTerminalSize(frame.Cols, frame.Rows) {
					_ = d.controlExec(cid, sid, frame)
				}
			}
		}
	}()
	go func() {
		header := make([]byte, 8)
		for {
			if _, err := io.ReadFull(reader, header); err != nil {
				done <- err
				return
			}
			size := binary.BigEndian.Uint32(header[4:])
			if size > 1024*1024 {
				done <- errors.New("Invalid Docker stream frame")
				return
			}
			data := make([]byte, size)
			if _, err := io.ReadFull(reader, data); err != nil {
				done <- err
				return
			}
			for len(data) > 0 {
				n := len(data)
				if n > 16384 {
					n = 16384
				}
				if err := send(terminalFrame{Type: "output", Data: base64.StdEncoding.EncodeToString(data[:n])}); err != nil {
					done <- err
					return
				}
				data = data[n:]
				select {
				case <-outputAck:
				case <-time.After(10 * time.Second):
					done <- errors.New("Terminal output stalled")
					return
				case <-r.Context().Done():
					return
				}
			}
		}
	}()
	if e = send(terminalFrame{Type: "ready", Data: "Connected as user 1000 · /data"}); e != nil {
		return
	}
	// An API crash or network partition must not leave the shell running forever.
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(20 * time.Second)) })
	_ = ws.SetReadDeadline(time.Now().Add(20 * time.Second))
	for {
		select {
		case <-done:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			raw, _ := d.call(ctx, "GET", "/exec/"+eid+"/json", nil)
			var result struct{ ExitCode int }
			_ = json.Unmarshal(raw, &result)
			_ = send(terminalFrame{Type: "exit", Code: result.ExitCode})
			return
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if e = d.controlExec(cid, sid, terminalFrame{Type: "heartbeat"}); e != nil {
				return
			}
			if e = ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); e != nil {
				return
			}
		}
	}
}
