# Workload recovery progress

Updated October 27, 2026. Scope: one Docker Desktop Linux engine. Portal authentication enhancements remain deferred.

## Delivered

- Durable owner/admin backup jobs and a resource Backups tab for PostgreSQL, Valkey, S3 storage, six plain Linux workspaces and persistent custom container applications.
- Running compute is stopped while its owned volume is read, then resumed. A stopped source remains stopped. PostgreSQL is a **physical stopped-cluster snapshot**, including its roles and databases, rather than an online logical dump. Restore requires the exact original provider image. This workflow includes an interruption.
- Networkless short-lived backup helpers have no Docker socket or management credentials. Only the source volume and installation-owned backup store are mounted. Resource gates and current operation checks guard lifecycle mutations; interrupted helper containers are removed before a new attempt proceeds.
- Streamed AES-256-GCM archives use a random per-backup data key, encrypted separately in platform metadata using the installation encryption key. Record positions and backup identity are authenticated, including a mandatory terminal record. SHA-256 identifies the ciphertext.
- Restore checks the checksum and authenticates the complete archive before extracting anything into a **new empty owned volume**. Content, numeric ownership, permissions and timestamps are retained. Directory sticky/setgid bits are supported, including the PostgreSQL volume root. Links, special files and special permission bits on regular files are refused explicitly.
- Admission for the new resource still checks CPU/RAM and declared subscription storage. The restore operation is queued atomically with its metadata; it cannot race ordinary empty-resource provisioning. Existing volumes are never cleared or overwritten. Incomplete targets block Start and configuration updates; purge that target and restore again into a new resource.
- Saved data-service credentials are preserved in encrypted snapshots. Restored applications need their service bindings, hostname and network placement configured again. Source data is unchanged by restore. Owner-only archive deletion removes successful and partial artifacts and their saved key material, while retaining audit/history metadata.
- Helpers check physical free bytes/inodes and require 256 MiB extra headroom beyond their bounded archive allowance. This is a recovery-job safeguard, **not general disk quota enforcement**.

## Verification

`node scripts/test-workload-backups.mjs` passed **12 integration groups**:

1. Workspace files, ownership and permissions recover into a fresh volume.
2. Original and restored resource data are independent.
3. Stopped source desired state is preserved.
4. Unsupported links fail explicitly and source compute resumes.
5. Actual PostgreSQL rows recover into an independent cluster volume.
6. Valkey persisted values recover.
7. A signed S3 object round trip survives recovery with its account credentials.
8. A custom application retains its exact image and persistent visit counter.
9. Metadata exposes image/checksum information without passwords or data keys.
10. Stored ciphertext contains no known file plaintext and matches SHA-256.
11. Corrupt archives fail before extraction; incomplete targets cannot start.
12. Successful/failed jobs remove ephemeral helpers.

Go tests additionally cover modified/truncated/appended ciphertext, replay under another backup identity, traversal paths, nonempty targets, links and special entries. Go tests/vet and portal TypeScript/production build passed. Earlier test attempts found a PostgreSQL sticky-directory handling issue and a test-client authentication variable error; both were corrected. A crowded Docker default address pool required releasing completed fixture clones earlier in the suite. No unrelated networks were removed.

## Current limits and next work

Archives are bounded to **512 MiB of tar data and 100,000 entries**, without compression. Larger/sparse data, symlinks, external tablespaces, extended attributes, ACLs and arbitrary mount arrangements are outside this initial format. Only contents under the managed data mount are included; container writable layers, external mounts and image-declared unmanaged volumes are not recovery sources.

Artifacts remain in a local owned Docker volume. They require the platform metadata, installation encryption key and exact workload images for recovery. Off-host export, schedules, configurable retention, a complete installation recovery bundle, gateway CA recovery, image closure and a clean separate-host recovery drill remain open. The Backups tab currently offers create/list/restore; permanent archive removal is available through the owner API. SSH workspaces, bastions and desktops need identity-aware recovery before joining this supported backup list.

General physical storage inventory/admission and qualified hard quotas are the next core functionality slice. Wider daemon-crash, lease-loss and interrupted-restore qualification also remain. Do not treat successful same-host fresh-volume restores as disaster recovery or production qualification.
