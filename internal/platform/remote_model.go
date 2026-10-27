package platform

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"golang.org/x/crypto/ssh"
	"strings"
)

func init() {
	for _, d := range []struct{ id, name string }{{"ubuntu", "Ubuntu"}, {"debian", "Debian"}, {"alpine", "Alpine"}, {"fedora", "Fedora"}, {"rocky", "Rocky Linux"}, {"alma", "AlmaLinux"}} {
		Catalog = append(Catalog, CatalogItem{ID: "workspace-ssh-" + d.id, Name: d.name + " SSH workspace", Category: "Remote access", Description: "Private SSH, browser shell and persistent home. Connect through a Kzure bastion.", Image: "kzure/workspace-ssh-" + d.id + ":0.1.0", CPU: .5, RAM: .5, Storage: 2, Available: true, Persistent: true})
	}
	Catalog = append(Catalog, CatalogItem{ID: "bastion", Name: "Bastion gateway", Category: "Remote access", Description: "Audited SSH jump gateway and restricted native RDP tunnels to selected private targets.", Image: "kzure/bastion:0.1.0", Port: 2222, CPU: .5, RAM: .5, Storage: 1, Available: true, Persistent: true})
	Catalog = append(Catalog, CatalogItem{ID: "desktop-ubuntu", Name: "Ubuntu desktop", Category: "Remote access", Description: "Persistent Xfce desktop with private RDP access through a managed bastion.", Image: "kzure/desktop-ubuntu:0.1.0", CPU: 2, RAM: 4, Storage: 20, Persistent: true, Available: true})
}

func isSSHWorkspace(kind string) bool { return strings.HasPrefix(kind, "workspace-ssh-") }

func normalizeSSHKey(raw string) (string, string, error) {
	if len(raw) > 2048 {
		return "", "", errors.New("SSH public key is too long")
	}
	key, _, options, rest, e := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(raw)))
	if e != nil || len(options) > 0 || len(strings.TrimSpace(string(rest))) > 0 {
		return "", "", errors.New("Enter one OpenSSH public key without authorization options")
	}
	if key.Type() != ssh.KeyAlgoED25519 {
		return "", "", errors.New("Use an Ed25519 public key (ssh-keygen -t ed25519)")
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), ssh.FingerprintSHA256(key), nil
}

func newSSHHostKey() (string, error) {
	_, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return "", e
	}
	block, e := ssh.MarshalPrivateKey(private, "Kzure host identity")
	if e != nil {
		return "", e
	}
	return string(pem.EncodeToMemory(block)), nil
}

func hostPublicKey(private string) (string, string, error) {
	signer, e := ssh.ParsePrivateKey([]byte(private))
	if e != nil {
		return "", "", e
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), ssh.FingerprintSHA256(signer.PublicKey()), nil
}

const remoteSchema = `
ALTER TABLE resources DROP CONSTRAINT IF EXISTS resources_group_id_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS resources_active_name ON resources(group_id,name) WHERE state<>'Deleted';
CREATE TABLE IF NOT EXISTS ssh_keys(id text PRIMARY KEY,user_id text NOT NULL REFERENCES users(id),name text NOT NULL,public_key text NOT NULL,fingerprint text NOT NULL,revoked boolean NOT NULL DEFAULT false,created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(user_id,fingerprint));
CREATE TABLE IF NOT EXISTS workspace_ssh_keys(resource_id text NOT NULL REFERENCES resources(id),key_id text NOT NULL REFERENCES ssh_keys(id),granted_by text NOT NULL,PRIMARY KEY(resource_id,key_id));
CREATE TABLE IF NOT EXISTS bastion_targets(bastion_id text NOT NULL REFERENCES resources(id),target_id text NOT NULL REFERENCES resources(id),PRIMARY KEY(bastion_id,target_id));
DELETE FROM bastion_targets b USING resources r WHERE b.target_id=r.id AND r.state='Deleted';
CREATE TABLE IF NOT EXISTS remote_connections(id text PRIMARY KEY,bastion_id text NOT NULL REFERENCES resources(id),target_id text NOT NULL REFERENCES resources(id),user_id text NOT NULL REFERENCES users(id),key_id text NOT NULL REFERENCES ssh_keys(id),state text NOT NULL DEFAULT 'Active',created_at timestamptz NOT NULL DEFAULT now(),last_seen timestamptz NOT NULL DEFAULT now(),ended_at timestamptz,reason text NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS remote_connection_leases ON remote_connections(state,last_seen);
`
