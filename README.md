# secure-file-transfer

Instead of emailing attachments, using a shared drive, or spinning up a permanent file hosting service, this tool lets you create a private storage space on demand, drop a file in, and hand the customer a link that expires automatically. When the transfer is done, you tear the whole thing down — no lingering infrastructure, no ongoing cost, no data sitting around.

Each transfer is isolated, so sharing a contract with Client A and a report with Client B are completely independent — separate storage, separate access, no cross-contamination.

The security posture is intentional: every file is AES-256 encrypted before it leaves your machine, files are never public, links expire (default one hour), and there is no permanent credential that could be leaked or stolen. The moment the link expires or you tear down the workspace, access is gone.

The operational overhead is minimal by design — one command to set up, one command to share, one command to clean up. The setup burden for someone new to the tool is also a single script.

---

## How it works

A **workspace** is the unit of isolation — one per transfer, named after the customer or purpose.

```
gh workflow run terraform.yml -f action=apply -f workspace=acme-q1-report
  └─ provisions a private GCS bucket + signing service account for this workspace

# single file — AES-256 encrypted zip + signed URL + one-time password
./transfer/transfer upload --workspace acme-q1-report --file report.pdf

# folder — same, but packs all files first
./transfer/transfer pack --workspace acme-q1-report --folder ./documents

                      [ share URL with customer → one-click download ]

gh workflow run terraform.yml -f action=destroy -f workspace=acme-q1-report -f confirm_destroy=destroy
  └─ removes the bucket, the service account, and all files
  └─ (or let the scheduled cleanup handle it automatically after 36 hours)
```

Signed URLs are served from `storage.googleapis.com`, which enforces TLS 1.2 / 1.3 and strong ECDHE cipher suites.

---

## Prerequisites

- A GCP project
- `gcloud` CLI authenticated: `gcloud auth login && gcloud config set project <project_id>`
- `gh` CLI authenticated: `gh auth login`
- `jq` in PATH: `brew install jq` (or `apt install jq`) — used by `setup.sh`
- `terraform` CLI in PATH — used by `setup.sh` for the bootstrap step
- `go` 1.22+ in PATH — to build the CLI

---

## One-time setup

Run the bootstrap script — it handles everything automatically:

```bash
bash setup.sh
```

It will:
1. Enable the required GCP APIs
2. Create a GCS bucket for Terraform state
3. Apply bootstrap Terraform to create the `secureTransferSignBlob` signing role
4. Create a least-privilege custom IAM role scoped to exactly what Terraform needs
5. Create a GitHub Actions service account with that role
6. Configure Workload Identity Federation so GitHub Actions authenticates without any key file
7. Enable GCS Data Access Audit Logs so every file access is recorded
8. Set all repository secrets (`GCP_PROJECT_ID`, `GCP_SIGNING_MEMBERS`, `TF_STATE_BUCKET`, `WORKLOAD_IDENTITY_PROVIDER`, `GCP_SERVICE_ACCOUNT`)

Then build the CLI:

```bash
cd transfer && make build
```

---

## Workflow

### Provision + upload (single file)

```bash
gh workflow run terraform.yml -f action=apply -f workspace=acme-q1-report

./transfer/transfer upload --workspace acme-q1-report --file report.pdf
```

The file is wrapped in an AES-256 encrypted zip before upload. Output:

```
========================================================================
Shareable URL (expires 2026-03-13 12:00 UTC):

https://storage.googleapis.com/...

Integrity:  SHA-256 = 3b4c9f...
========================================================================

────────────────────────────────────────────────────────────────────────
PASSWORD — share via a separate channel, do NOT send with the URL:

Xk9mP2rL...
────────────────────────────────────────────────────────────────────────
```

Share the URL and checksum by one channel, the password by another. The recipient unzips with 7-Zip — see [Encryption and unzipping](#encryption-and-unzipping).

### Provision + pack (folder)

```bash
gh workflow run terraform.yml -f action=apply -f workspace=acme-q1-report

./transfer/transfer pack --workspace acme-q1-report --folder ./documents
```

Use `--prefix` to organize multiple uploads in the same workspace under a subfolder:

```bash
./transfer/transfer pack --workspace acme-q1-report --folder ./q1 --prefix q1
./transfer/transfer pack --workspace acme-q1-report --folder ./q2 --prefix q2
```

### Tear down a workspace

```bash
gh workflow run terraform.yml -f action=destroy -f workspace=acme-q1-report -f confirm_destroy=destroy
```

Typing `destroy` in `confirm_destroy` is required — it prevents accidental teardown.

### Automatic workspace expiry

Workspaces self-destruct automatically. A cleanup workflow runs twice daily (06:00 and 18:00 UTC) and destroys any workspace whose bucket is older than the TTL (default **36 hours**). Worst case: a workspace is torn down within 12 hours of its TTL expiring.

| Layer | What happens | When |
|-------|-------------|------|
| Signed URL | Expires, link stops working | After 1 hour (default) |
| Files in bucket | Auto-deleted by lifecycle policy | After 24 hours |
| Whole workspace | Bucket + service account + IAM torn down | After 36 hours |

To trigger cleanup immediately, or with a custom TTL:

```bash
# Destroy all workspaces older than 36h (default)
gh workflow run cleanup.yml

# Destroy all workspaces older than 12h
gh workflow run cleanup.yml -f ttl_hours=12
```

You can still tear down a specific workspace manually at any time — automatic expiry is a safety net, not a replacement.

### Tear down the project entirely

When you are done with the tool and want a clean GCP account, destroy all active workspaces first (or use `cleanup.yml` to do it in bulk), then tear down the long-lived bootstrap resources:

```bash
terraform -chdir=terraform/bootstrap init -backend-config="bucket=<project_id>-tf-state"
terraform -chdir=terraform/bootstrap destroy -var="project_id=<project_id>"
```

This removes the `secureTransferSignBlob` custom role. The Terraform state bucket, WIF configuration, and GitHub Actions service account were created by `setup.sh` and must be removed manually via `gcloud` if desired.

### Running multiple transfers in parallel

Each workspace is fully isolated. Run as many as needed simultaneously:

```bash
gh workflow run terraform.yml -f action=apply -f workspace=acme-q1-report
gh workflow run terraform.yml -f action=apply -f workspace=globex-contract
```

Each gets its own bucket (`secure-transfer-<workspace>`) and can be torn down independently.

---

## Encryption and unzipping

Both `upload` and `pack` wrap their payload in an AES-256 encrypted zip before uploading. Use `pack` when you have multiple files — it zips the entire folder (preserving directory structure) before encrypting.

The output is identical for both commands — split deliberately so you can share the URL and password via different channels (e.g. email + IM).

The recipient unzips with any AES-256 compatible tool. macOS's built-in `unzip` does **not** support AES-256 — use 7-Zip:

```bash
# macOS (install once)
brew install p7zip

# Extract
7z x -p<password> <file>.zip
```

On Windows, 7-Zip or WinZip work. On Linux, `7z` (from `p7zip-full`) or `unzip` from InfoZIP 6.1+.

---

## All flags

```bash
# Override the default 1h expiry (max 24h)
./transfer/transfer upload --workspace acme-q1-report --file report.pdf --expiry 4h
./transfer/transfer pack   --workspace acme-q1-report --folder ./documents --expiry 1d

# Store under a subfolder inside the bucket
./transfer/transfer upload --workspace acme-q1-report --file report.pdf --prefix invoices

# List files currently in a workspace's bucket
./transfer/transfer list --workspace acme-q1-report

# List with prefix filter
./transfer/transfer list --workspace acme-q1-report --prefix invoices

# Delete a specific file before it expires (uploaded files are stored as .zip)
./transfer/transfer delete --workspace acme-q1-report --object report.pdf.zip --confirm report.pdf.zip

# Machine-readable JSON output for scripting
./transfer/transfer upload --workspace acme-q1-report --file report.pdf --json
./transfer/transfer upload --workspace acme-q1-report --file report.pdf --json | jq .url
```

The `--json` flag outputs a single JSON object:

```json
{
  "url": "https://storage.googleapis.com/...",
  "filename": "report.pdf.zip",
  "sha256": "3b4c9f...",
  "expires_at": "2026-03-13T13:00:00Z",
  "password": "Xk9mP2rL..."
}
```

---

## Building

```bash
cd transfer

make build          # macOS (current platform)
make build-linux    # Linux amd64 static binary
make build-windows  # Windows amd64 .exe
make all            # all three platforms
make test           # run unit tests
make clean          # remove build artifacts
```

All binaries are built with `CGO_ENABLED=0` — fully static, no libc dependency.

### Environment variables

| Variable | Description |
|---|---|
| `TRANSFER_GCP_PROJECT` | Override the GCP project ID (skips ADC project lookup — useful in CI or multi-project environments) |

---

## Known limitations

Several security findings from code reviews were deliberately not addressed. Each is documented as a GitHub issue with the rationale:

- [#4 — Retention policy on transfer bucket](../../issues/4)
- [#5 — Plan-time validation of bootstrap custom role](../../issues/5)
- [#6 — Soft-deleted custom role detection in setup.sh](../../issues/6)
- [#7 — Separate upload and signing IAM permissions](../../issues/7)

---

## Security notes

- **No credentials at rest** — GitHub Actions authenticates via Workload Identity Federation. No service account key is ever created or stored.
- **Least-privilege deployer** — the GitHub Actions service account holds a custom role with only the 19 permissions Terraform needs. No broad `storage.admin` or `iam.admin`.
- **Keyless URL signing** — the CLI impersonates the per-workspace signing SA via the IAM `signBlob` API using your local ADC credentials, revocable at any time.
- **No accidental public access** — buckets have `public_access_prevention = enforced` and uniform bucket-level access; objects can never be made public.
- **Signed URLs are read-only and time-limited** — scoped to `GET` only, expire at the requested time (default 1h, max 24h).
- **Encryption at rest and in transit** — every upload (single file or folder) is wrapped in an AES-256 encrypted zip with a randomly generated 32-character password. The password is printed separately so it can be shared via a different channel from the URL.
- **File integrity** — a SHA-256 checksum is computed in-flight while streaming to GCS (single pass, no extra file read) and printed alongside the signed URL. Recipients can verify the file was not modified in transit or at rest.
- **Audit trail** — GCS Data Access Audit Logs (READ + WRITE) are enabled at the project level. Every file access is recorded in Cloud Audit Logs.
- **Automatic cleanup** — files auto-delete after 1 day; the entire workspace (bucket, service account, IAM bindings) is torn down after 36 hours by a scheduled cleanup workflow.
