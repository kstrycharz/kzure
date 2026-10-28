package main

import (
	"encoding/base64"
	"kzure.local/platform/internal/platform"
	"log"
	"os"
)

func main() {
	mode := "api"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "bastion" {
		log.Fatal(platform.RunBastion())
		return
	}
	if mode == "edge" {
		log.Fatal(platform.RunEdge())
		return
	}
	if mode == "edge-status" {
		if err := platform.EdgeStatus(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if (mode == "backup-pack" || mode == "backup-unpack" || mode == "backup-delete") && len(os.Args) == 3 {
		if err := platform.RunBackupTool(mode, os.Args[2]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if mode == "tls-init" && len(os.Args) == 4 {
		if e := platform.GenerateLocalTLS(os.Args[2], os.Args[3]); e != nil {
			log.Fatal(e)
		}
		log.Print("Local HTTPS certificate files generated")
		return
	}
	if mode == "connector" && len(os.Args) == 4 {
		log.Fatal(platform.RunConnector(os.Args[2], os.Args[3]))
		return
	}
	a := &platform.App{AgentURL: os.Getenv("KZURE_AGENT_URL"), AgentToken: os.Getenv("KZURE_AGENT_TOKEN"), InstallID: os.Getenv("KZURE_INSTALL_ID"), PublicHost: os.Getenv("KZURE_PUBLIC_HOST")}
	if mode == "agent" {
		log.Fatal(a.RunAgent())
		return
	}
	var e error
	a.Key, e = base64.StdEncoding.DecodeString(os.Getenv("KZURE_ENCRYPTION_KEY"))
	if e != nil || len(a.Key) != 32 {
		log.Fatal("KZURE_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	a.DB, e = platform.OpenDB(os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal(e)
	}
	defer a.DB.Close()
	switch mode {
	case "reset-admin":
		if e = a.ResetAdmin(); e != nil {
			log.Fatal(e)
		}
		log.Print("Administrator password reset; previous browser sessions revoked")
	case "api":
		if e = a.Migrate(); e != nil {
			log.Fatal(e)
		}
		log.Fatal(a.RunAPI())
	case "worker":
		log.Fatal(a.RunWorker())
	default:
		log.Fatal("usage: kzure [api|worker|agent]")
	}
}
