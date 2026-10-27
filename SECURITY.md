# Security and private installation data

Kzure is a single-host development preview for a trusted local operator. Keep management services on loopback. See the documented release gates before enabling LAN access or admitting untrusted tenants.

The Docker agent controls the host Docker engine. Protect its credentials and socket access. Workspace containers share the host kernel and are not VM isolation boundaries. Network security groups currently qualify IPv4 traffic inside the Docker Desktop Linux engine only.

Never commit or upload `.env`, `.local`, backups, workload data, database dumps, SSH private keys, TLS keys, generated credentials, screenshots of private configuration, or operator logs. The ignore rules exclude these artifacts. Retain the installation encryption key securely with the metadata backup; workload volumes need separate backups.

Before publishing changes, stage only intended source and run `node scripts/audit-repository.mjs`. It checks the Git index against this installation's current secret values and private artifact patterns, then writes an ignored source export for an independent secret scanner. Scan both that export and the resulting Git history with a maintained scanner using redacted output. Review the complete tracked file list as well. Automated scanning reduces risk; it is not a guarantee against every kind of sensitive data.

Do not place credentials or private environment details in GitHub issues, commits, Actions logs or pull requests. Report security issues privately to the repository owner rather than including exploitation details or secrets in a public issue. A production vulnerability-reporting process is not established for this preview.
