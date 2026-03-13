#!/usr/bin/env bash
# setup.sh — one-time bootstrap for a new fork of secure-file-transfer
#
# What this script does:
#   1. Enables required GCP APIs
#   2. Creates a GCS bucket for Terraform state
#   3. Creates a least-privilege custom IAM role for the deployer
#   4. Creates a GitHub Actions service account with that role
#   5. Configures Workload Identity Federation (no SA key ever created)
#   6. Enables GCS Data Access Audit Logs
#   7. Sets all GitHub Actions secrets
#
# Prerequisites:
#   - gcloud CLI authenticated:  gcloud auth login && gcloud config set project <project_id>
#   - gh CLI authenticated:      gh auth login
#   - python3 in PATH

set -euo pipefail

# ---------------------------------------------------------------------------
# Config
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
REPO=$(gh repo view --json nameWithOwner --jq .nameWithOwner)
PROJECT_NUMBER=$(gcloud projects describe "$GCP_PROJECT" --format="value(projectNumber)")
WIF_POOL="github-pool"
WIF_PROVIDER="github-provider"
WIF_PROVIDER_FULL="projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${WIF_POOL}/providers/${WIF_PROVIDER}"

echo "Project    : $GCP_PROJECT"
echo "Signer     : $GCP_SIGNING_MEMBER"
echo "State      : gs://$STATE_BUCKET"
echo "Repository : $REPO"
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
  sts.googleapis.com \
  logging.googleapis.com \
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
# 3. Least-privilege custom IAM role
#    Scoped to exactly what Terraform needs — no storage.admin or iam.admin.
# ---------------------------------------------------------------------------
echo ""
echo "==> Creating least-privilege deployer role..."
PERMISSIONS="storage.buckets.create,storage.buckets.delete,\
storage.buckets.get,storage.buckets.getIamPolicy,storage.buckets.list,\
storage.buckets.setIamPolicy,storage.buckets.update,\
storage.objects.create,storage.objects.delete,\
storage.objects.get,storage.objects.list,\
iam.serviceAccounts.create,iam.serviceAccounts.delete,\
iam.serviceAccounts.get,iam.serviceAccounts.getIamPolicy,\
iam.serviceAccounts.list,iam.serviceAccounts.setIamPolicy,\
resourcemanager.projects.get"

if gcloud iam roles describe SecureTransferDeployer --project="$GCP_PROJECT" &>/dev/null; then
  echo "    Role already exists — updating permissions..."
  gcloud iam roles update SecureTransferDeployer \
    --project="$GCP_PROJECT" \
    --permissions="$PERMISSIONS" \
    --quiet
else
  gcloud iam roles create SecureTransferDeployer \
    --project="$GCP_PROJECT" \
    --title="Secure Transfer Deployer" \
    --description="Least-privilege role for secure-file-transfer GitHub Actions" \
    --permissions="$PERMISSIONS"
fi

# ---------------------------------------------------------------------------
# 4. GitHub Actions service account
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

echo "==> Granting custom deployer role..."
gcloud projects add-iam-policy-binding "$GCP_PROJECT" \
  --member="serviceAccount:$SA_EMAIL" \
  --role="projects/$GCP_PROJECT/roles/SecureTransferDeployer" \
  --quiet

# Remove broad legacy roles if they were previously granted
for legacy in roles/storage.admin roles/iam.serviceAccountAdmin roles/iam.serviceAccountTokenCreator; do
  gcloud projects remove-iam-policy-binding "$GCP_PROJECT" \
    --member="serviceAccount:$SA_EMAIL" \
    --role="$legacy" \
    --quiet 2>/dev/null || true
done

# ---------------------------------------------------------------------------
# 5. Workload Identity Federation — no SA key is ever created or stored
# ---------------------------------------------------------------------------
echo ""
echo "==> Configuring Workload Identity Federation..."

if gcloud iam workload-identity-pools describe "$WIF_POOL" \
    --project="$GCP_PROJECT" --location=global &>/dev/null; then
  echo "    WIF pool already exists — skipping."
else
  gcloud iam workload-identity-pools create "$WIF_POOL" \
    --project="$GCP_PROJECT" \
    --location=global \
    --display-name="GitHub Actions Pool"
fi

if gcloud iam workload-identity-pools providers describe "$WIF_PROVIDER" \
    --project="$GCP_PROJECT" --location=global \
    --workload-identity-pool="$WIF_POOL" &>/dev/null; then
  echo "    WIF provider already exists — skipping."
else
  gcloud iam workload-identity-pools providers create-oidc "$WIF_PROVIDER" \
    --project="$GCP_PROJECT" \
    --location=global \
    --workload-identity-pool="$WIF_POOL" \
    --display-name="GitHub Actions OIDC Provider" \
    --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository" \
    --issuer-uri="https://token.actions.githubusercontent.com" \
    --attribute-condition="attribute.repository=='${REPO}'"
fi

echo "==> Binding repository to service account..."
gcloud iam service-accounts add-iam-policy-binding "$SA_EMAIL" \
  --project="$GCP_PROJECT" \
  --role="roles/iam.workloadIdentityUser" \
  --member="principalSet://iam.googleapis.com/projects/${PROJECT_NUMBER}/locations/global/workloadIdentityPools/${WIF_POOL}/attribute.repository/${REPO}" \
  --quiet

# ---------------------------------------------------------------------------
# 6. GCS Data Access Audit Logs
#    Adds READ + WRITE audit log config to the project IAM policy.
# ---------------------------------------------------------------------------
echo ""
echo "==> Enabling GCS Data Access Audit Logs..."
gcloud projects get-iam-policy "$GCP_PROJECT" --format=json | python3 - "$GCP_PROJECT" << 'PYEOF'
import sys, json, subprocess, tempfile, os

project = sys.argv[1]
policy  = json.load(sys.stdin)

svc = "storage.googleapis.com"
existing = policy.setdefault("auditConfigs", [])

if any(c.get("service") == svc for c in existing):
    print("    Audit logs already configured — skipping.")
    sys.exit(0)

existing.append({
    "service": svc,
    "auditLogConfigs": [
        {"logType": "DATA_READ"},
        {"logType": "DATA_WRITE"},
    ],
})

with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as f:
    json.dump(policy, f)
    tmp = f.name

subprocess.run(
    ["gcloud", "projects", "set-iam-policy", project, tmp, "--quiet"],
    check=True,
)
os.unlink(tmp)
print("    Audit logs enabled.")
PYEOF

# ---------------------------------------------------------------------------
# 7. GitHub Actions secrets
#    No GCP_CREDENTIALS — WIF handles authentication keylessly.
# ---------------------------------------------------------------------------
echo ""
echo "==> Setting GitHub Actions secrets..."
gh secret set GCP_PROJECT_ID             --body "$GCP_PROJECT"
gh secret set TF_STATE_BUCKET            --body "$STATE_BUCKET"
gh secret set GCP_SIGNING_MEMBERS        --body "[\"$GCP_SIGNING_MEMBER\"]"
gh secret set WORKLOAD_IDENTITY_PROVIDER --body "$WIF_PROVIDER_FULL"
gh secret set GCP_SERVICE_ACCOUNT        --body "$SA_EMAIL"

# Remove legacy key secret if it exists
gh secret delete GCP_CREDENTIALS 2>/dev/null && echo "    Removed legacy GCP_CREDENTIALS secret." || true

# ---------------------------------------------------------------------------
echo ""
echo "Done. Secrets set:"
gh secret list
echo ""
echo "Next steps:"
echo "  1. Install Python deps:  cd scripts && python -m venv .venv && source .venv/bin/activate && pip install -r requirements.txt"
echo "  2. Provision:            gh workflow run terraform.yml -f action=apply -f workspace=<name>"
echo "  3. Upload:               python scripts/transfer.py upload --workspace <name> --file <file>"
