# Start over with a fresh Kzure installation

The explicit reset workflow is for an operator who wants to permanently discard the current installation. It removes this checkout's Kzure services, workload/helper containers, managed volumes, backup archives, owned networks and installation firewall chains. It removes `.env` and the entire `.local` directory, including passwords, encryption keys, local certificates, installation identity and private reports. Existing users, resources and historical metadata are discarded with the metadata volume.

Source code, Git history and cached Docker images are kept. Other Docker projects are not pruned. Ownership labels and Compose checkout paths are checked before deletion. The script refuses a linked `.local` directory, a mismatched Compose checkout or firewall references that cannot be attributed safely to this installation. If object removal fails, it preserves the private configuration for inspection instead of generating a new identity over surviving objects.

Use **PowerShell 7+**, Docker with Linux containers and Node.js 24+:

```powershell
./scripts/reinstall.ps1 -WipeData
```

The `-WipeData` switch is mandatory. This action is permanent; it does not create a recovery backup. The workflow rebuilds catalog/platform images, generates new random administrator/database passwords and service/encryption keys, generates a fresh local portal certificate authority and starts the services. It verifies the new administrator login, one default Home lab tenant/group and zero existing resources. It does not seed demonstration workloads or change the computer's certificate trust store.

Open `http://localhost:8088`. The new credentials are saved privately in `.local/admin-credentials.txt`; the script does not print the password. Optional HTTPS is at `https://localhost:8443` with the newly generated local certificate. Previous certificate trust, workload credentials, SSH host identities and sessions belong to the discarded installation.

For ordinary rebuilds that preserve data and credentials, continue using `scripts/start.ps1` instead. `scripts/reset-admin.mjs` changes only the administrator password when a complete reinstall is unnecessary.
