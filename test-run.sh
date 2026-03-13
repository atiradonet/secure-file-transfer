#!/usr/bin/env bash
# test-run.sh — end-to-end walkthrough of the secure-file-transfer workflow.
#
# Runs through: provision → upload (single file) → pack (folder) → verify → tear down
# Takes about 6 minutes.

set -euo pipefail

WORKSPACE="test-run-$(date +%s)"
TEST_DIR="/tmp/${WORKSPACE}"
TEST_FILE="/tmp/${WORKSPACE}-single.txt"

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
# since: ISO timestamp — only accept downloads after the upload completed.
check_download_occurred() {
  local workspace=$1
  local since=$2
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
     AND protoPayload.authenticationInfo.principalEmail=\"${signing_sa}\" \
     AND timestamp>=\"${since}\"" \
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

# Build the Go binary
TRANSFER_BIN="$(dirname "$0")/transfer/transfer"
step "Building Go binary"
if ! command -v go &>/dev/null; then
  echo "Error: go not found in PATH"
  exit 1
fi
(cd "$(dirname "$0")/transfer" && go build -o transfer .)
ok "Binary built: $TRANSFER_BIN"

# ---------------------------------------------------------------------------
# Step 1 — Create test fixtures
# ---------------------------------------------------------------------------
step "1 / 5  Create test fixtures"

echo "secure-file-transfer single-file test — $(date -u)" > "$TEST_FILE"
ok "Created single file: $TEST_FILE"

mkdir -p "$TEST_DIR/subfolder"
echo "secure-file-transfer test run — workspace: $WORKSPACE — $(date -u)" > "$TEST_DIR/readme.txt"
echo "top-level file" > "$TEST_DIR/document.txt"
echo "nested file"   > "$TEST_DIR/subfolder/nested.txt"
ok "Created folder: $TEST_DIR with 3 files (including subfolder)"

# ---------------------------------------------------------------------------
# Step 2 — Provision workspace
# ---------------------------------------------------------------------------
step "2 / 5  Provision workspace: $WORKSPACE"
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
# Step 3 — Upload single file
# ---------------------------------------------------------------------------
step "3 / 5  Upload single file"

UPLOAD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
"$TRANSFER_BIN" upload \
  --workspace "$WORKSPACE" \
  --file "$TEST_FILE" \
  --expiry 30m

echo ""
ask "Download the zip above, enter the password, confirm the file contains the expected text — then press Enter"

# ---------------------------------------------------------------------------
# Step 4 — Pack folder
# ---------------------------------------------------------------------------
step "4 / 5  Pack folder"

"$TRANSFER_BIN" pack \
  --workspace "$WORKSPACE" \
  --folder "$TEST_DIR" \
  --expiry 30m

echo ""
ask "Download the zip above, enter the password, verify all 3 files and the subfolder — then press Enter"

# ---------------------------------------------------------------------------
# Step 5 — Tear down
# ---------------------------------------------------------------------------
step "5 / 5  Tear down workspace: $WORKSPACE"
check_download_occurred "$WORKSPACE" "$UPLOAD_TIME"
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
rm -rf "$TEST_DIR" "$TEST_FILE"
echo ""
echo "══════════════════════════════════════════════════════════════════════"
echo "  Test run complete. All steps passed."
echo "══════════════════════════════════════════════════════════════════════"
