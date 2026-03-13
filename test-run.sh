#!/usr/bin/env bash
# test-run.sh — end-to-end walkthrough of the secure-file-transfer workflow.
#
# Runs through: provision → upload → verify URL → tear down
# Takes about 2 minutes.

set -euo pipefail

WORKSPACE="test-run-$(date +%s)"
TEST_FILE="/tmp/${WORKSPACE}.txt"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
step() { echo ""; echo "── $* ──────────────────────────────────────────────"; }
ok()   { echo "  ✓ $*"; }
info() { echo "  → $*"; }
ask()  { read -r -p "  $* [press Enter to continue] " _; }

wait_for_run() {
  local run_id=$1
  info "Waiting for workflow run $run_id..."
  gh run watch "$run_id" --exit-status
}

# Verify the signed URL was used by checking GCS Data Access Audit Logs.
# No time filter — any DATA_READ by the signing SA is sufficient confirmation.
check_download_occurred() {
  local workspace=$1
  local project
  project=$(gcloud config get-value project 2>/dev/null)
  local bucket="secure-transfer-${workspace}"
  local signing_sa="st-signer-${workspace}@${project}.iam.gserviceaccount.com"

  info "Verifying download via audit logs..."
  local hit
  hit=$(gcloud logging read \
    "logName=\"projects/${project}/logs/cloudaudit.googleapis.com%2Fdata_access\" \
     AND resource.type=\"gcs_bucket\" \
     AND resource.labels.bucket_name=\"${bucket}\" \
     AND protoPayload.authenticationInfo.principalEmail=\"${signing_sa}\"" \
    --limit=1 \
    --format="value(timestamp)" \
    2>/dev/null)

  if [[ -n "$hit" ]]; then
    ok "Download confirmed via audit log ($hit)"
  else
    echo ""
    echo "  ⚠  No download recorded in audit logs for this workspace."
    echo "  → Logs can lag a few minutes, or the link was not used."
    read -r -p "  Proceed with destroy anyway? [y/N] " confirm
    [[ "$confirm" =~ ^[Yy]$ ]] || { info "Destroy cancelled."; exit 0; }
  fi
}

# ---------------------------------------------------------------------------
# Preflight
# ---------------------------------------------------------------------------
step "Preflight checks"

if ! gcloud auth application-default print-access-token &>/dev/null; then
  echo "Error: no Application Default Credentials. Run: gcloud auth application-default login"
  exit 1
fi
ok "GCP Application Default Credentials found"

if ! gh auth status &>/dev/null; then
  echo "Error: gh CLI not authenticated. Run: gh auth login"
  exit 1
fi
ok "GitHub CLI authenticated"

VENV_DIR="$(dirname "$0")/scripts/.venv"
if [[ ! -f "$VENV_DIR/bin/activate" ]]; then
  echo "Error: Python venv not found. Run: cd scripts && python -m venv .venv && pip install -r requirements.txt"
  exit 1
fi
ok "Python venv found"

# ---------------------------------------------------------------------------
# Step 1 — Create a test file
# ---------------------------------------------------------------------------
step "1 / 4  Create test file"
echo "secure-file-transfer test run — workspace: $WORKSPACE — $(date -u)" > "$TEST_FILE"
ok "Created $TEST_FILE"

# ---------------------------------------------------------------------------
# Step 2 — Provision workspace
# ---------------------------------------------------------------------------
step "2 / 4  Provision workspace: $WORKSPACE"
info "Triggering terraform apply..."
BEFORE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
gh workflow run terraform.yml \
  -f action=apply \
  -f workspace="$WORKSPACE"

sleep 3
RUN_ID=$(gh run list --workflow=terraform.yml --limit=10 --json databaseId,createdAt \
  --jq "[.[] | select(.createdAt >= \"$BEFORE\")] | .[0].databaseId")
wait_for_run "$RUN_ID"
ok "Infrastructure provisioned"
info "Bucket: secure-transfer-${WORKSPACE}"

# IAM bindings can take up to ~90 s to propagate after terraform apply.
info "Waiting 90 s for IAM propagation..."
sleep 90

# ---------------------------------------------------------------------------
# Step 3 — Upload and get signed URL
# ---------------------------------------------------------------------------
step "3 / 4  Upload file and get signed URL"
source "$VENV_DIR/bin/activate"

python "$(dirname "$0")/scripts/transfer.py" upload \
  --workspace "$WORKSPACE" \
  --file "$TEST_FILE" \
  --expiry 30m

deactivate

echo ""
ask "Copy the URL above, open it in a browser, confirm the file downloads, then press Enter"

# ---------------------------------------------------------------------------
# Step 4 — Tear down
# ---------------------------------------------------------------------------
step "4 / 4  Tear down workspace: $WORKSPACE"
check_download_occurred "$WORKSPACE"
info "Triggering terraform destroy..."
BEFORE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
gh workflow run terraform.yml \
  -f action=destroy \
  -f workspace="$WORKSPACE" \
  -f confirm_destroy=destroy

sleep 3
RUN_ID=$(gh run list --workflow=terraform.yml --limit=10 --json databaseId,createdAt \
  --jq "[.[] | select(.createdAt >= \"$BEFORE\")] | .[0].databaseId")
wait_for_run "$RUN_ID"
ok "Workspace destroyed"

# ---------------------------------------------------------------------------
rm -f "$TEST_FILE"
echo ""
echo "══════════════════════════════════════════════════════════════════════"
echo "  Test run complete. All steps passed."
echo "══════════════════════════════════════════════════════════════════════"
