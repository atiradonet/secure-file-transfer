# secure-file-transfer

Disposable file-sharing using Google Cloud Storage signed URLs — spin up, upload, share, tear down.

## How it works

1. `terraform apply` — provisions a private GCS bucket and a signing service account
2. `transfer.py upload` — uploads a file and returns a time-limited signed URL
3. Share the URL with the customer; clicking it triggers an immediate download
4. `terraform destroy` — removes all infrastructure and files

Signed URLs are served directly from `storage.googleapis.com`, which enforces TLS 1.2 / 1.3 and strong ECDHE cipher suites. No service-account key file is ever created.

---

## Prerequisites

- GCP project with these APIs enabled:
  ```bash
  gcloud services enable storage.googleapis.com \
      iam.googleapis.com \
      iamcredentials.googleapis.com
  ```
- A GCS bucket for Terraform state (create once):
  ```bash
  gsutil mb -p <project_id> gs://<project_id>-tf-state
  gsutil versioning set on gs://<project_id>-tf-state
  ```
- A GCP service account with `roles/editor` (or narrower: Storage Admin + IAM Admin) and a JSON key exported for GitHub Actions auth.
- Python 3.9+ and `gcloud` CLI (for running `transfer.py` locally).

---

## GitHub Actions secrets

Set these in **Settings → Secrets and variables → Actions**:

| Secret | Value |
|---|---|
| `GCP_PROJECT_ID` | your GCP project ID |
| `GCP_CREDENTIALS` | contents of the service account JSON key |
| `GCP_SIGNING_MEMBERS` | JSON array of IAM members, e.g. `["user:you@gmail.com"]` |
| `TF_STATE_BUCKET` | name of the GCS bucket created above for Terraform state |

---

## Setup

**1. Provision infrastructure** — go to **Actions → Secure File Transfer — Infrastructure → Run workflow**, choose `apply`.

After the run, check the workflow logs for the two Terraform outputs:
```
bucket_name      = "secure-transfer-a1b2c3d4"
signing_sa_email = "secure-transfer-signer@my-project.iam.gserviceaccount.com"
```

**2. Install the Python dependencies** (once, on your local machine):
```bash
cd scripts
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
```

---

## Usage

### Upload and get a shareable URL

The workspace name is the only identifier needed — bucket and signing SA are derived automatically.

```bash
python transfer.py upload --workspace acme-q1-report --file report.pdf --expiry 1h
```

```
Uploading  report.pdf  →  gs://secure-transfer-acme-q1-report/report.pdf
Upload complete.

========================================================================
Shareable URL (expires 2025-06-15 14:32 UTC):

https://storage.googleapis.com/secure-transfer-acme-q1-report/report.pdf?X-Goog-...
========================================================================
```

Or as a single chained command:

```bash
gh workflow run terraform.yml -f action=apply -f workspace=acme-q1-report && \
python transfer.py upload --workspace acme-q1-report --file report.pdf --expiry 1h
```

### List files in the bucket

```bash
python transfer.py list --workspace acme-q1-report
```

### Delete a file early

```bash
python transfer.py delete --workspace acme-q1-report --object report.pdf
```

---

## Tear down

Go to **Actions → Secure File Transfer — Infrastructure → Run workflow**, choose `destroy`.

All objects and infrastructure are removed.

---

## Security notes

- The bucket has `public_access_prevention = enforced` — objects can never be made public accidentally.
- Signed URLs are scoped to `GET` only and expire at the requested time (max 7 days).
- No service-account key file is generated. The script calls the IAM `signBlob` API by impersonating the signing SA using your ADC — revocable at any time via IAM.
- Files auto-delete after `file_retention_days` (default: 7) even if you forget to clean up.
