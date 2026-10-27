package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

type BastionConfig struct {
	ID      string `json:"id"`
	API     string `json:"api"`
	Token   string `json:"token"`
	HostKey string `json:"hostKey"`
}

func (c BastionConfig) authorize(in BastionRequest) (BastionResponse, error) {
	var out BastionResponse
	raw, _ := json.Marshal(in)
	req, e := http.NewRequest("POST", c.API+"/api/internal/bastions/"+c.ID+"/authorize", bytes.NewReader(raw))
	if e != nil {
		return out, e
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-Kzure-Request", "portal")
	req.Header.Set("Content-Type", "application/json")
	res, e := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	if e != nil {
		return out, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return out, errors.New("Kzure access check denied")
	}
	e = json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&out)
	if e != nil || !out.Allowed {
		return out, errors.New("Kzure access check failed")
	}
	return out, nil
}

func RunBastion() error {
	raw, e := os.ReadFile("/run/kzure/bastion.json")
	if e != nil {
		return e
	}
	var c BastionConfig
	if e = json.Unmarshal(raw, &c); e != nil {
		return e
	}
	signer, e := ssh.ParsePrivateKey([]byte(c.HostKey))
	if e != nil {
		return e
	}
	config := &ssh.ServerConfig{MaxAuthTries: 3, PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		fp := ssh.FingerprintSHA256(key)
		out, e := c.authorize(BastionRequest{Action: "auth", Email: meta.User(), Fingerprint: fp})
		if e != nil {
			return nil, e
		}
		return &ssh.Permissions{Extensions: map[string]string{"fingerprint": fp, "userId": out.UserID}}, nil
	}}
	config.AddHostKey(signer)
	listener, e := net.Listen("tcp", ":2222")
	if e != nil {
		return e
	}
	defer listener.Close()
	log.Print("Kzure bastion: restricted SSH forwarding on :2222")
	slots := make(chan struct{}, 64)
	for {
		conn, e := listener.Accept()
		if e != nil {
			return e
		}
		select {
		case slots <- struct{}{}:
			go func() {
				defer func() { <-slots }()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				server, channels, requests, e := ssh.NewServerConn(conn, config)
				if e != nil {
					return
				}
				defer server.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Hour))
				go ssh.DiscardRequests(requests)
				channelSlots := make(chan struct{}, 5)
				for channel := range channels {
					if channel.ChannelType() != "direct-tcpip" {
						_ = channel.Reject(ssh.Prohibited, "Only authorized target forwarding is available")
						continue
					}
					select {
					case channelSlots <- struct{}{}:
						go func(ch ssh.NewChannel) { defer func() { <-channelSlots }(); forwardBastion(c, server, ch) }(channel)
					default:
						_ = channel.Reject(ssh.ResourceShortage, "Too many forwarded connections")
					}
				}
			}()
		default:
			conn.Close()
		}
	}
}

func forwardBastion(c BastionConfig, server *ssh.ServerConn, in ssh.NewChannel) {
	var p struct {
		Host       string
		Port       uint32
		Origin     string
		OriginPort uint32
	}
	if ssh.Unmarshal(in.ExtraData(), &p) != nil || p.Port > 65535 {
		_ = in.Reject(ssh.Prohibited, "Invalid forwarding request")
		return
	}
	grant := BastionRequest{Action: "connect", Email: server.User(), Fingerprint: server.Permissions.Extensions["fingerprint"], Target: p.Host, Port: int(p.Port), SessionID: id()}
	out, e := c.authorize(grant)
	if e != nil {
		_ = in.Reject(ssh.Prohibited, "Target access denied")
		return
	}
	defer func() { grant.Action = "close"; _, _ = c.authorize(grant) }()
	backend, e := net.DialTimeout("tcp", out.Address, 5*time.Second)
	if e != nil {
		_ = in.Reject(ssh.ConnectionFailed, "Target is unavailable")
		return
	}
	defer backend.Close()
	channel, requests, e := in.Accept()
	if e != nil {
		return
	}
	defer channel.Close()
	go ssh.DiscardRequests(requests)
	var lastInput atomic.Int64
	lastInput.Store(time.Now().UnixNano())
	done := make(chan struct{}, 2)
	go func() {
		buf := make([]byte, 32768)
		for {
			n, e := channel.Read(buf)
			if n > 0 {
				lastInput.Store(time.Now().UnixNano())
				_ = backend.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if _, err := backend.Write(buf[:n]); err != nil {
					break
				}
			}
			if e != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() { _, _ = io.Copy(channel, backend); done <- struct{}{} }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	max := time.NewTimer(2 * time.Hour)
	defer max.Stop()
	for {
		select {
		case <-done:
			return
		case <-max.C:
			return
		case <-ticker.C:
			if time.Since(time.Unix(0, lastInput.Load())) > 15*time.Minute {
				return
			}
			grant.Action = "check"
			if _, e = c.authorize(grant); e != nil {
				return
			}
		}
	}
}

// The connector accepts TCP only inside its managed Docker networks. Destination
// and port are fixed at creation, with no HTTP CONNECT or dynamic routing API.
func RunConnector(address, port string) error {
	if _, e := strconv.Atoi(port); e != nil {
		return e
	}
	if _, _, e := net.SplitHostPort(address); e != nil {
		return e
	}
	ln, e := net.Listen("tcp", ":"+port)
	if e != nil {
		return e
	}
	defer ln.Close()
	slots := make(chan struct{}, 64)
	for {
		source, e := ln.Accept()
		if e != nil {
			return e
		}
		select {
		case slots <- struct{}{}:
			go func() {
				defer func() { <-slots }()
				defer source.Close()
				target, e := net.DialTimeout("tcp", address, 5*time.Second)
				if e != nil {
					return
				}
				defer target.Close()
				_ = source.SetDeadline(time.Now().Add(30 * time.Minute))
				_ = target.SetDeadline(time.Now().Add(30 * time.Minute))
				var once sync.Once
				stop := func() { once.Do(func() { source.Close(); target.Close() }) }
				go func() { _, _ = io.Copy(target, source); stop() }()
				_, _ = io.Copy(source, target)
				stop()
			}()
		default:
			source.Close()
		}
	}
}

var _ = fmt.Sprint
