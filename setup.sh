#!/usr/bin/env bash
# setup.sh — one-time bootstrap for a new fork of secure-file-transfer
#
# What this script does:
#   1. Enables required GCP APIs
#   2. Creates a GCS bucket for Terraform state
#   3. Applies bootstrap Terraform (creates the secureTransferSignBlob signing role)
#   4. Creates a least-privilege custom IAM role for the deployer
#   5. Creates a GitHub Actions service account with that role
#   6. Configures Workload Identity Federation (no SA key ever created)
#   7. Enables GCS Data Access Audit Logs
#   8. Sets all GitHub Actions secrets
#
# Prerequisites:
#   - gcloud CLI authenticated:  gcloud auth login && gcloud config set project <project_id>
#   - gh CLI authenticated:      gh auth login
#   - jq in PATH:                brew install jq  (or apt install jq)

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

if ! command -v jq &>/dev/null; then
  echo "Error: jq not found in PATH. Install with: brew install jq  (or apt install jq)"
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
# 3. Bootstrap Terraform — long-lived project-scoped resources
#    Creates the secureTransferSignBlob custom role once at the project level.
#    Must run before any workspace terraform apply.
# ---------------------------------------------------------------------------
echo ""
echo "==> Applying bootstrap Terraform..."
terraform -chdir=terraform/bootstrap init \
  -backend-config="bucket=$STATE_BUCKET" \
  -reconfigure \
  -input=false \
  -no-color

terraform -chdir=terraform/bootstrap apply \
  -var="project_id=$GCP_PROJECT" \
  -auto-approve \
  -input=false \
  -no-color

# ---------------------------------------------------------------------------
# 4. Least-privilege custom IAM role
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
# 5. GitHub Actions service account
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
# 6. Workload Identity Federation — no SA key is ever created or stored
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
# 7. GCS Data Access Audit Logs
#    Adds READ + WRITE audit log config to the project IAM policy.
# ---------------------------------------------------------------------------
echo ""
echo "==> Enabling GCS Data Access Audit Logs..."
POLICY_FILE=$(mktemp /tmp/iam-policy-XXXXXX.json)
trap 'rm -f "$POLICY_FILE"' RETURN
gcloud projects get-iam-policy "$GCP_PROJECT" --format=json > "$POLICY_FILE"

if jq -e '.auditConfigs[]? | select(.service == "storage.googleapis.com")' \
    "$POLICY_FILE" > /dev/null 2>&1; then
  echo "    Audit logs already configured — skipping."
else
  jq '.auditConfigs |= (. // []) + [{
    "service": "storage.googleapis.com",
    "auditLogConfigs": [
      {"logType": "DATA_READ"},
      {"logType": "DATA_WRITE"}
    ]
  }]' "$POLICY_FILE" > "${POLICY_FILE}.new" && mv "${POLICY_FILE}.new" "$POLICY_FILE"
  gcloud projects set-iam-policy "$GCP_PROJECT" "$POLICY_FILE" --quiet
  echo "    Audit logs enabled."
fi
rm -f "$POLICY_FILE"

# ---------------------------------------------------------------------------
# 8. GitHub Actions secrets
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
echo "  1. Build the CLI:  cd transfer && make build"
echo "  2. Provision:      gh workflow run terraform.yml -f action=apply -f workspace=<name>"
echo "  3. Upload:         ./transfer/transfer upload --workspace <name> --file <file>"
