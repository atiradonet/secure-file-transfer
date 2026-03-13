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

- `terraform` >= 1.5
- `gcloud` CLI authenticated: `gcloud auth application-default login`
- Python 3.9+
- GCP project with these APIs enabled:
  ```bash
  gcloud services enable storage.googleapis.com \
      iam.googleapis.com \
      iamcredentials.googleapis.com
  ```

---

## Setup

```bash
cd terraform
cp terraform.tfvars.example terraform.tfvars
# edit terraform.tfvars — set project_id and your user in signing_sa_members

terraform init
terraform apply
```

Note the two outputs:
```
bucket_name      = "secure-transfer-a1b2c3d4"
signing_sa_email = "secure-transfer-signer@my-project.iam.gserviceaccount.com"
```

Install the Python dependencies once:
```bash
cd scripts
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
```

---

## Usage

### Upload and get a shareable URL

```bash
python transfer.py upload \
    --bucket      secure-transfer-a1b2c3d4 \
    --signing-sa  secure-transfer-signer@my-project.iam.gserviceaccount.com \
    --file        report.pdf \
    --expiry      48h
```

```
Uploading  report.pdf  →  gs://secure-transfer-a1b2c3d4/report.pdf
Upload complete.

========================================================================
Shareable URL (expires 2025-06-15 14:32 UTC):

https://storage.googleapis.com/secure-transfer-a1b2c3d4/report.pdf?X-Goog-...
========================================================================
```

Send that URL to your customer. It expires automatically at the requested time.

### List files in the bucket

```bash
python transfer.py list \
    --bucket     secure-transfer-a1b2c3d4 \
    --signing-sa secure-transfer-signer@...
```

### Delete a file early

```bash
python transfer.py delete \
    --bucket     secure-transfer-a1b2c3d4 \
    --signing-sa secure-transfer-signer@... \
    --object     report.pdf
```

---

## Tear down

```bash
cd terraform
terraform destroy
```

All objects and infrastructure are removed.

---

## Security notes

- The bucket has `public_access_prevention = enforced` — objects can never be made public accidentally.
- Signed URLs are scoped to `GET` only and expire at the requested time (max 7 days).
- No service-account key file is generated. The script calls the IAM `signBlob` API by impersonating the signing SA using your ADC — revocable at any time via IAM.
- Files auto-delete after `file_retention_days` (default: 7) even if you forget to clean up.
