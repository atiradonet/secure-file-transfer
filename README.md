# secure-file-transfer

Instead of emailing attachments, using a shared drive, or spinning up a permanent file hosting service, this tool lets you create a private storage space on demand, drop a file in, and hand the customer a link that expires automatically. When the transfer is done, you tear the whole thing down — no lingering infrastructure, no ongoing cost, no data sitting around.

Each transfer is isolated, so sharing a contract with Client A and a report with Client B are completely independent — separate storage, separate access, no cross-contamination.

The security posture is intentional: files are never public, links expire (default one hour), and there is no permanent credential that could be leaked or stolen. The moment the link expires or you tear down the workspace, access is gone.

The operational overhead is minimal by design — one command to set up, one command to share, one command to clean up. The setup burden for someone new to the tool is also a single script.

---

## How it works

A **workspace** is the unit of isolation — one per transfer, named after the customer or purpose.

```
gh workflow run terraform.yml -f action=apply -f workspace=acme-q1-report
  └─ provisions a private GCS bucket + signing service account for this workspace

python scripts/transfer.py upload --workspace acme-q1-report --file report.pdf
  └─ uploads the file and prints a time-limited signed URL

                      [ share URL with customer → one-click download ]

gh workflow run terraform.yml -f action=destroy -f workspace=acme-q1-report -f confirm_destroy=destroy
  └─ removes the bucket, the service account, and all files
```

Signed URLs are served from `storage.googleapis.com`, which enforces TLS 1.2 / 1.3 and strong ECDHE cipher suites.

---

## Prerequisites

- A GCP project
- `gcloud` CLI authenticated: `gcloud auth login && gcloud config set project <project_id>`
- `gh` CLI authenticated: `gh auth login`
- `python3` in PATH

---

## One-time setup

Run the bootstrap script — it handles everything automatically:

```bash
bash setup.sh
```

It will:
1. Enable the required GCP APIs
2. Create a GCS bucket for Terraform state
3. Create a least-privilege custom IAM role scoped to exactly what Terraform needs
4. Create a GitHub Actions service account with that role
5. Configure Workload Identity Federation so GitHub Actions authenticates without any key file
6. Enable GCS Data Access Audit Logs so every file access is recorded
7. Set all repository secrets (`GCP_PROJECT_ID`, `GCP_SIGNING_MEMBERS`, `TF_STATE_BUCKET`, `WORKLOAD_IDENTITY_PROVIDER`, `GCP_SERVICE_ACCOUNT`)

Then install the Python dependencies:

```bash
cd scripts && python -m venv .venv && source .venv/bin/activate && pip install -r requirements.txt
```

---

## Workflow

### Provision + upload in one command

```bash
gh workflow run terraform.yml -f action=apply -f workspace=acme-q1-report && \
python scripts/transfer.py upload --workspace acme-q1-report --file report.pdf
```

The script prints the signed URL to share with the customer. The URL expires after 1 hour by default.

### Tear down

```bash
gh workflow run terraform.yml -f action=destroy -f workspace=acme-q1-report -f confirm_destroy=destroy
```

Typing `destroy` in `confirm_destroy` is required — it prevents accidental teardown.

### Running multiple transfers in parallel

Each workspace is fully isolated. Run as many as needed simultaneously:

```bash
gh workflow run terraform.yml -f action=apply -f workspace=acme-q1-report
gh workflow run terraform.yml -f action=apply -f workspace=globex-contract
```

Each gets its own bucket (`secure-transfer-<workspace>`) and can be torn down independently.

---

## Other script commands

```bash
# List files currently in a workspace's bucket
python scripts/transfer.py list --workspace acme-q1-report

# Delete a specific file before it expires
python scripts/transfer.py delete --workspace acme-q1-report --object report.pdf

# Override the default 1h expiry (max 7d)
python scripts/transfer.py upload --workspace acme-q1-report --file report.pdf --expiry 4h
```

---

## Security notes

- **No credentials at rest** — GitHub Actions authenticates via Workload Identity Federation. No service account key is ever created or stored.
- **Least-privilege deployer** — the GitHub Actions service account holds a custom role with only the 19 permissions Terraform needs. No broad `storage.admin` or `iam.admin`.
- **Keyless URL signing** — the script impersonates the per-workspace signing SA via the IAM `signBlob` API using your local ADC credentials, revocable at any time.
- **No accidental public access** — buckets have `public_access_prevention = enforced` and uniform bucket-level access; objects can never be made public.
- **Signed URLs are read-only and time-limited** — scoped to `GET` only, expire at the requested time (default 1h, max 7d).
- **Audit trail** — GCS Data Access Audit Logs (READ + WRITE) are enabled at the project level. Every file access is recorded in Cloud Audit Logs.
- **Automatic cleanup** — files auto-delete after 7 days even if the workspace is not explicitly destroyed.
