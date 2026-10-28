# Fresh installation verification

Completed October 27, 2026, at the user's explicit request to discard all Kzure configuration/data and start over.

The code checkpoint was pushed before reset. The reset removed 24 installation-owned workload/helper containers, 17 managed data/archive volumes, 19 managed networks, the six platform containers, platform metadata volume and Compose networks. Installation firewall hooks/chains were removed after ownership checks. The previous `.env` and entire private `.local` directory were then deleted, including old credentials, local certificates, reports and metadata backup files. No recovery copy was retained because a complete wipe was requested.

The catalog/platform images were rebuilt, and a new empty installation was started with newly generated installation identity, administrator/database passwords, agent token, encryption key and portal TLS certificates. All five private identity/credential settings were compared in memory with their prior values and confirmed changed; their values were not printed or published. Unrelated Docker containers were preserved.

Verified results:

- Portal, API, metadata database, worker, agent and optional HTTPS gateway are running.
- New administrator password works through the API and the actual browser sign-in form.
- Fresh certificate authority and hostname validation pass over HTTPS; login issues a Secure session cookie.
- Docker host reports connected, with zero workload CPU, RAM and storage reservations.
- Database inventory: **one user, one tenant, one default resource group; zero resources, operations or backups**.
- Portal displays the empty Home lab dashboard and zero estimated workload costs. No demonstration workloads were seeded.
- Twenty-four current catalog image references are locked; the previous installation's image-history lock entries are absent.

One PowerShell empty-array check interrupted the first reset pass after old Docker objects were removed and before private configuration deletion. The check was fixed in `6870005`, pushed, and the reset completed successfully on rerun. The feature checkpoint `56e6578` passed GitHub backend/portal CI; its workload recovery suite passed 12 groups before the wipe. Historical private reports were intentionally discarded by this reset; their scope/results remain documented in [workload recovery progress](WORKLOAD_RECOVERY_PROGRESS.md).

Private fresh-install and HTTPS verification reports are in the new `.local` directory. New credentials are in `.local/admin-credentials.txt`; those files are ignored by Git. The repeatable destructive procedure is documented in [FRESH_INSTALL.md](FRESH_INSTALL.md). This local reinstall does not qualify the outstanding separate-Linux-host, disaster-recovery or hardware gates.
