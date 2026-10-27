package platform

import (
	"strings"
	"testing"
)

func TestApplicationLogsWithoutSecretsAreAlsoBounded(t *testing.T) {
	out := redactApplicationValues(strings.Repeat("z", 2*1024*1024), nil)
	if len(out) > 1024*1024+32 || !strings.HasSuffix(out, "[output truncated]") {
		t.Fatal("plain application log output was not bounded")
	}
}

func TestApplicationLogRedactionIsBoundedAndNonRecursive(t *testing.T) {
	if got := redactApplicationValues("secret redacted", []string{"secret", "redacted"}); got != "[redacted] [redacted]" {
		t.Fatal("redaction recursively modified its own marker")
	}
	out := redactApplicationValues(strings.Repeat("a", 2*1024*1024), []string{"a"})
	if len(out) > 1024*1024+32 || !strings.HasSuffix(out, "[output truncated]") {
		t.Fatal("expanded secret redaction was not bounded")
	}
}

func TestStoppedUnhealthyContainerIsStopped(t *testing.T) {
	var in inspection
	in.State.Health = &struct{ Status string }{Status: "unhealthy"}
	d := &dockerClient{}
	if got := d.observation(in).State; got != "Stopped" {
		t.Fatalf("stopped container reported %s", got)
	}
	in.State.Running = true
	if got := d.observation(in).State; got != "Degraded" {
		t.Fatalf("unhealthy running container reported %s", got)
	}
}

func TestApplicationConfigurationBoundaries(t *testing.T) {
	valid := Spec{CPU: .5, RAM: .5, Storage: 1, MountPath: "/data", Image: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Port: 3000, Environment: map[string]string{"GREETING": "hello"}, SecretEnvironment: map[string]string{"TOKEN": strings.Repeat("ab", 16)}}
	if e := validateSpec("container-app", "example", valid); e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct {
		name   string
		change func(*Spec)
	}{
		{"unresolved image", func(s *Spec) { s.Image = "unapproved:latest" }},
		{"system mount", func(s *Spec) { s.MountPath = "/etc/secret" }},
		{"path traversal", func(s *Spec) { s.MountPath = "/data/../etc" }},
		{"host bind syntax", func(s *Spec) { s.MountPath = "/data:/etc" }},
		{"worker listener", func(s *Spec) { s.Mode = "worker" }},
		{"secret collision", func(s *Spec) { s.Environment = map[string]string{"TOKEN": "not-a-secret-reference"} }},
		{"invalid variable", func(s *Spec) { s.Environment = map[string]string{"A=B": "value"} }},
		{"volume without capacity", func(s *Spec) { s.Storage = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := valid
			test.change(&s)
			if e := validateSpec("container-app", "example", s); e == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}
