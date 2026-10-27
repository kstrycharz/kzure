package platform

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"time"
)

type App struct {
	DB                                          DB
	AgentURL, AgentToken, InstallID, PublicHost string
	Key                                         []byte
}
type CatalogItem struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Image       string   `json:"image"`
	Port        int      `json:"port"`
	CPU         float64  `json:"cpu"`
	RAM         float64  `json:"ram"`
	Storage     float64  `json:"storage"`
	Available   bool     `json:"available"`
	Reason      string   `json:"reason,omitempty"`
	Persistent  bool     `json:"persistent"`
	Command     []string `json:"-"`
}
type Spec struct {
	CPU               float64           `json:"cpu"`
	RAM               float64           `json:"ram"`
	Storage           float64           `json:"storage"`
	HTML              string            `json:"html,omitempty"`
	Image             string            `json:"image,omitempty"`
	SubnetID          string            `json:"subnetId,omitempty"`
	Mode              string            `json:"mode,omitempty"`
	Port              int               `json:"port,omitempty"`
	Environment       map[string]string `json:"environment,omitempty"`
	SecretEnvironment map[string]string `json:"secretEnvironment,omitempty"`
	Command           []string          `json:"command,omitempty"`
	Healthcheck       []string          `json:"healthcheck,omitempty"`
	MountPath         string            `json:"mountPath,omitempty"`
	ServiceBindings   []string          `json:"serviceBindings,omitempty"`
}
type Resource struct {
	Revision        int       `json:"revision"`
	AssignedPort    int       `json:"assignedPort,omitempty"`
	ID              string    `json:"id"`
	TenantID        string    `json:"tenantId"`
	SubscriptionID  string    `json:"subscriptionId"`
	GroupID         string    `json:"groupId"`
	GroupName       string    `json:"groupName"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	State           string    `json:"state"`
	Desired         string    `json:"desired"`
	Spec            Spec      `json:"spec"`
	ContainerID     string    `json:"containerId,omitempty"`
	Endpoint        string    `json:"endpoint,omitempty"`
	Error           string    `json:"error,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	MonthlyEstimate float64   `json:"monthlyEstimate"`
	Accrued         float64   `json:"accrued"`
	Volume          string    `json:"volume,omitempty"`
}
type Operation struct {
	ID           string    `json:"id"`
	ResourceID   string    `json:"resourceId"`
	ResourceName string    `json:"resourceName"`
	Action       string    `json:"action"`
	State        string    `json:"state"`
	Message      string    `json:"message"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Admin bool   `json:"admin"`
}
type ProvisionRequest struct {
	Restore  bool              `json:"restore,omitempty"`
	Resource Resource          `json:"resource"`
	Secrets  map[string]string `json:"secrets,omitempty"`
	Network  *NetworkPlan      `json:"network,omitempty"`
	SSHKeys  []string          `json:"sshKeys,omitempty"`
}
type Observation struct {
	ContainerID string  `json:"containerId"`
	State       string  `json:"state"`
	Endpoint    string  `json:"endpoint"`
	Volume      string  `json:"volume"`
	Health      string  `json:"health,omitempty"`
	MemoryBytes uint64  `json:"memoryBytes,omitempty"`
	CPUPercent  float64 `json:"cpuPercent,omitempty"`
}
type Host struct {
	Name             string  `json:"name"`
	CPUs             int     `json:"cpus"`
	RAM              float64 `json:"ram"`
	OS               string  `json:"os"`
	Architecture     string  `json:"architecture"`
	Version          string  `json:"version"`
	ReservedCPU      float64 `json:"reservedCpu"`
	ReservedRAM      float64 `json:"reservedRam"`
	HardStorageQuota bool    `json:"hardStorageQuota"`
	GPU              bool    `json:"gpu"`
	Connected        bool    `json:"connected"`
}

var Catalog = []CatalogItem{
	{ID: "static-web", Name: "Static web app", Category: "Web", Description: "Publish a website from HTML, served by your own Nginx container.", Image: "kzure/static-web:0.1.0", Port: 8080, CPU: .25, RAM: .25, Available: true},
	{ID: "container-app", Name: "Container app", Category: "Compute", Description: "A managed web container with health checks and a local endpoint.", Image: "nginxinc/nginx-unprivileged:1.29-alpine", Port: 8080, CPU: .5, RAM: .5, Available: true},
	{ID: "postgres", Name: "PostgreSQL", Category: "Databases", Description: "A dedicated database server with encrypted credentials and persistent storage.", Image: "postgres:18-alpine", Port: 5432, CPU: 1, RAM: 1, Storage: 10, Available: true, Persistent: true},
	{ID: "valkey", Name: "Valkey cache", Category: "Databases", Description: "An authenticated, persistent Redis-compatible cache.", Image: "valkey/valkey:8-alpine", Port: 6379, CPU: .5, RAM: .5, Storage: 1, Available: true, Persistent: true},
	{ID: "storage", Name: "Storage account", Category: "Storage", Description: "S3-compatible object storage running locally with dedicated access keys.", Image: "kzure/storage-seaweed:0.1.0", Port: 8333, CPU: .5, RAM: 1, Storage: 10, Available: true, Persistent: true},
	{ID: "workspace-ubuntu", Name: "Ubuntu workspace", Category: "Linux", Description: "Ubuntu 24.04 container userland with a persistent data volume.", Image: "kzure/workspace-ubuntu:0.1.0", CPU: .5, RAM: .5, Storage: 2, Available: true, Persistent: true, Command: []string{"sleep", "infinity"}},
	{ID: "workspace-debian", Name: "Debian workspace", Category: "Linux", Description: "Debian container workspace sharing the host Linux kernel.", Image: "kzure/workspace-debian:0.1.0", CPU: .5, RAM: .5, Storage: 2, Available: true, Persistent: true, Command: []string{"sleep", "infinity"}},
	{ID: "workspace-alpine", Name: "Alpine workspace", Category: "Linux", Description: "A lightweight Alpine Linux workspace with persistent data.", Image: "kzure/workspace-alpine:0.1.0", CPU: .25, RAM: .25, Storage: 1, Available: true, Persistent: true, Command: []string{"sleep", "infinity"}},
	{ID: "workspace-fedora", Name: "Fedora workspace", Category: "Linux", Description: "Fedora Linux container workspace on local hardware.", Image: "kzure/workspace-fedora:0.1.0", CPU: .5, RAM: .5, Storage: 2, Available: true, Persistent: true, Command: []string{"sleep", "infinity"}},
	{ID: "workspace-rocky", Name: "Rocky Linux workspace", Category: "Linux", Description: "Rocky Linux 9 workspace with a retained data volume.", Image: "kzure/workspace-rocky:0.1.0", CPU: .5, RAM: .5, Storage: 2, Available: true, Persistent: true, Command: []string{"sleep", "infinity"}},
	{ID: "workspace-alma", Name: "AlmaLinux workspace", Category: "Linux", Description: "AlmaLinux 9 container workspace for enterprise Linux workloads.", Image: "kzure/workspace-alma:0.1.0", CPU: .5, RAM: .5, Storage: 2, Available: true, Persistent: true, Command: []string{"sleep", "infinity"}},
	{ID: "waf", Name: "Web application firewall", Category: "Security", Description: "ModSecurity and OWASP CRS policy gateway.", CPU: .5, RAM: .5, Available: false, Reason: "Provider and isolated origin routing are under development."},
	{ID: "gpu", Name: "GPU compute", Category: "Compute", Description: "CUDA compute with an exclusive physical GPU reservation.", CPU: 4, RAM: 8, Storage: 10, Available: false, Reason: "Requires qualified NVIDIA device inventory and exclusive-allocation tests on this host."},
	{ID: "paas", Name: "App service", Category: "Web", Description: "Build and host Node.js, Python, .NET, and Java applications.", CPU: 1, RAM: 1, Available: false, Reason: "Source builds and runtime providers are under development."},
}

func Item(id string) (CatalogItem, error) {
	for _, c := range Catalog {
		if c.ID == id {
			return c, nil
		}
	}
	return CatalogItem{}, errors.New("unknown resource type")
}

var validName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,47}$`)

func validateSpec(kind, name string, s Spec) error {
	c, e := Item(kind)
	if e != nil {
		return e
	}
	if !c.Available {
		return errors.New(c.Reason)
	}
	if !validName.MatchString(name) {
		return errors.New("name must contain 2–48 lowercase letters, digits or hyphens and start with a letter")
	}
	if math.IsNaN(s.CPU) || math.IsInf(s.CPU, 0) || s.CPU < .1 || s.CPU > 128 {
		return errors.New("CPU must be between 0.1 and 128")
	}
	if math.IsNaN(s.RAM) || math.IsInf(s.RAM, 0) || s.RAM < .125 || s.RAM > 1024 {
		return errors.New("RAM must be between 0.125 and 1024 GiB")
	}
	if s.Storage < 0 || s.Storage > 100000 || math.IsNaN(s.Storage) || math.IsInf(s.Storage, 0) {
		return errors.New("invalid storage allocation")
	}
	if c.Persistent && s.Storage < 1 {
		return errors.New("persistent services require at least 1 GiB of declared storage")
	}
	if kind == "bastion" && (s.RAM < .5 || s.CPU < .5) {
		return errors.New("Bastion requires at least 0.5 vCPU and 0.5 GiB RAM for its gateway and protocol proxy")
	}
	if kind == "bastion" && s.SubnetID != "" {
		return errors.New("Bastions use a dedicated gateway network; attach private targets after deployment")
	}
	if kind == "desktop-ubuntu" && (s.RAM < 2 || s.CPU < 1) {
		return errors.New("Desktop requires at least 1 vCPU and 2 GiB RAM")
	}
	if len(s.HTML) > 1024*1024 {
		return errors.New("HTML must be less than 1 MiB")
	}
	if kind == "container-app" {
		if e := validateAppSpec(s); e != nil {
			return e
		}
	} else if s.Mode != "" || s.Port != 0 || len(s.Environment) > 0 || len(s.SecretEnvironment) > 0 || len(s.Command) > 0 || len(s.Healthcheck) > 0 || s.MountPath != "" {
		return errors.New("application settings are supported only for container apps")
	}
	if kind != "container-app" && s.Image != "" && s.Image != c.Image {
		return errors.New("only the approved catalog image is allowed in this release")
	}
	if kind == "postgres" && s.RAM < .5 {
		return errors.New("PostgreSQL requires at least 0.5 GiB RAM")
	}
	return nil
}
func monthly(s Spec, running bool) float64 {
	v := s.Storage * .00014
	if running {
		v += s.CPU*.01 + s.RAM*.002
	}
	return v * 730
}
func id() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func (a *App) seal(v map[string]string) (string, error) {
	b, e := aes.NewCipher(a.Key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return "", e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return "", e
	}
	p, _ := json.Marshal(v)
	return base64.StdEncoding.EncodeToString(g.Seal(n, n, p, nil)), nil
}
func (a *App) unseal(s string) (map[string]string, error) {
	out := map[string]string{}
	if s == "" {
		return out, nil
	}
	v, e := base64.StdEncoding.DecodeString(s)
	if e != nil {
		return nil, e
	}
	b, e := aes.NewCipher(a.Key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	if len(v) < g.NonceSize() {
		return nil, errors.New("invalid encrypted secret")
	}
	p, e := g.Open(nil, v[:g.NonceSize()], v[g.NonceSize():], nil)
	if e != nil {
		return nil, errors.New("secret decryption failed")
	}
	e = json.Unmarshal(p, &out)
	return out, e
}
func secretsFor(kind string) map[string]string {
	if kind == "desktop-ubuntu" {
		return map[string]string{"username": "kzure", "password": token()}
	}
	if isSSHWorkspace(kind) || kind == "bastion" {
		private, e := newSSHHostKey()
		if e != nil {
			panic(e)
		}
		return map[string]string{"hostKey": private, "controlToken": token()}
	}
	switch kind {
	case "postgres":
		return map[string]string{"username": "kzure", "database": "app", "password": token()}
	case "valkey":
		return map[string]string{"password": token()}
	case "storage":
		return map[string]string{"accessKey": strings.ToUpper(id()[:20]), "secretKey": token()}
	}
	return map[string]string{}
}
func requiredEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		panic(fmt.Sprintf("%s is required", k))
	}
	return v
}
