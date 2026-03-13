#!/usr/bin/env bash
# setup.sh — one-time bootstrap for a new fork of secure-file-transfer
#
# What this script does:
#   1. Enables required GCP APIs
#   2. Creates a GCS bucket for Terraform state
#   3. Creates a GitHub Actions service account with the necessary roles
#   4. Sets all four GitHub Actions secrets
#
# Prerequisites:
#   - gcloud CLI authenticated:  gcloud auth login && gcloud config set project <project_id>
#   - gh CLI authenticated:      gh auth login
#   - Both CLIs in PATH

set -euo pipefail

# ---------------------------------------------------------------------------
# Config — edit these before running
# ---------------------------------------------------------------------------
GCP_PROJECT="${GCP_PROJECT:-$(gcloud config get-value project 2>/dev/null)}"
GCP_SIGNING_MEMBER="${GCP_SIGNING_MEMBER:-user:$(gcloud config get-value account 2>/dev/null)}"

# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------
if [[ -z "$GCP_PROJECT" ]]; then
  echo "Error: GCP project not set. Run: gcloud config set project <project_id>"
  exit 1
fi

if ! gh auth status &>/dev/null; then
  echo "Error: gh CLI not authenticated. Run: gh auth login"
  exit 1
fi

SA_EMAIL="github-actions-deployer@${GCP_PROJECT}.iam.gserviceaccount.com"
STATE_BUCKET="${GCP_PROJECT}-tf-state"
KEY_FILE="$(mktemp /tmp/gha-credentials-XXXXXX.json)"

echo "Project  : $GCP_PROJECT"
echo "Signer   : $GCP_SIGNING_MEMBER"
echo "State    : gs://$STATE_BUCKET"
echo ""
read -r -p "Proceed? [y/N] " confirm
[[ "$confirm" =~ ^[Yy]$ ]] || exit 0

# ---------------------------------------------------------------------------
# 1. Enable APIs
# ---------------------------------------------------------------------------
echo ""
echo "==> Enabling GCP APIs..."
gcloud services enable \
  storage.googleapis.com \
  iam.googleapis.com \
  iamcredentials.googleapis.com \
  cloudresourcemanager.googleapis.com \
  --project="$GCP_PROJECT"

echo "    Waiting 30s for API enablement to propagate..."
sleep 30

# ---------------------------------------------------------------------------
# 2. Terraform state bucket
# ---------------------------------------------------------------------------
echo ""
echo "==> Creating Terraform state bucket..."
if gsutil ls -p "$GCP_PROJECT" "gs://$STATE_BUCKET" &>/dev/null; then
  echo "    Already exists — skipping."
else
  gsutil mb -p "$GCP_PROJECT" "gs://$STATE_BUCKET"
  gsutil versioning set on "gs://$STATE_BUCKET"
fi

# ---------------------------------------------------------------------------
# 3. GitHub Actions service account
# ---------------------------------------------------------------------------
echo ""
echo "==> Creating GitHub Actions service account..."
if gcloud iam service-accounts describe "$SA_EMAIL" --project="$GCP_PROJECT" &>/dev/null; then
  echo "    Already exists — skipping creation."
else
  gcloud iam service-accounts create github-actions-deployer \
    --project="$GCP_PROJECT" \
    --display-name="GitHub Actions Deployer"
fi

echo "==> Granting IAM roles..."
for role in roles/storage.admin roles/iam.serviceAccountAdmin roles/iam.serviceAccountTokenCreator; do
  gcloud projects add-iam-policy-binding "$GCP_PROJECT" \
    --member="serviceAccount:$SA_EMAIL" \
    --role="$role" \
    --quiet
done

echo "==> Creating service account key..."
gcloud iam service-accounts keys create "$KEY_FILE" \
  --iam-account="$SA_EMAIL" \
  --project="$GCP_PROJECT"

# ---------------------------------------------------------------------------
# 4. GitHub Actions secrets
# ---------------------------------------------------------------------------
echo ""
echo "==> Setting GitHub Actions secrets..."
gh secret set GCP_PROJECT_ID      --body "$GCP_PROJECT"
gh secret set TF_STATE_BUCKET     --body "$STATE_BUCKET"
gh secret set GCP_SIGNING_MEMBERS --body "[\"$GCP_SIGNING_MEMBER\"]"
gh secret set GCP_CREDENTIALS     < "$KEY_FILE"

# Key file is only needed for the secret — remove it immediately
rm -f "$KEY_FILE"
echo "    Key file deleted."

# ---------------------------------------------------------------------------
echo ""
echo "Done. All secrets are set:"
gh secret list
echo ""
echo "Next steps:"
echo "  1. Install Python deps:  cd scripts && python -m venv .venv && source .venv/bin/activate && pip install -r requirements.txt"
echo "  2. Provision:            gh workflow run terraform.yml -f action=apply -f workspace=<name>"
echo "  3. Upload:               python scripts/transfer.py upload --workspace <name> --file <file>"
