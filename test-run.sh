#!/usr/bin/env bash
# test-run.sh — end-to-end walkthrough of the secure-file-transfer workflow.
#
# Runs through: provision → upload (single file + JSON validation) → pack (with prefix) → verify → tear down
# Takes about 8-10 minutes.

set -euo pipefail

WORKSPACE="test-run-$(date +%s)"
TEST_DIR="/tmp/${WORKSPACE}"
TEST_FILE="/tmp/${WORKSPACE}-single.txt"

# Clean up test fixtures on exit (covers both success and early failure).
cleanup() { rm -rf "$TEST_DIR" "$TEST_FILE"; }
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
step() { echo ""; echo "── $* ──────────────────────────────────────────────"; }
ok()   { echo "  ✓ $*"; }
info() { echo "  → $*"; }
ask()  { read -r -p "  $* [press Enter to continue] " _; }

wait_for_run() {
  local run_id=$1
  if [[ -z "$run_id" || "$run_id" == "null" ]]; then
    echo "Error: could not find the workflow run. Check 'gh run list' manually."
    exit 1
  fi
  info "Waiting for workflow run $run_id..."
  gh run watch "$run_id" --exit-status
}

# Resolve the most recent workflow run created at or after $1 (ISO timestamp).
# Retries for up to 15 s in case GitHub hasn't listed the run yet.
get_run_id() {
  local since=$1 run_id=""
  for _ in 1 2 3 4 5; do
    run_id=$(gh run list --workflow=terraform.yml --limit=10 \
      --json databaseId,createdAt \
      --jq "[.[] | select(.createdAt >= \"$since\")] | .[0].databaseId")
    [[ -n "$run_id" && "$run_id" != "null" ]] && break
    sleep 3
  done
  echo "$run_id"
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

# Resolve and export the project so the Go binary finds it regardless of
# whether it is embedded in ADC. GOOGLE_CLOUD_PROJECT is the standard GCP
# env var recognised by all GCP SDKs.
GOOGLE_CLOUD_PROJECT="$(gcloud config get-value project 2>/dev/null)"
if [[ -z "$GOOGLE_CLOUD_PROJECT" ]]; then
  echo "Error: GCP project not set. Run: gcloud config set project <project_id>"
  exit 1
fi
export GOOGLE_CLOUD_PROJECT
ok "GCP project: $GOOGLE_CLOUD_PROJECT"

if ! gh auth status &>/dev/null; then
  echo "Error: gh CLI not authenticated. Run: gh auth login"
  exit 1
fi
ok "GitHub CLI authenticated"

if ! command -v jq &>/dev/null; then
  echo "Error: jq not found in PATH. Install with: brew install jq"
  exit 1
fi
ok "jq found"

# Build the Go binary via the Makefile (CGO_ENABLED=0 static binary).
TRANSFER_BIN="$(dirname "$0")/transfer/transfer"
step "Building Go binary"
if ! command -v go &>/dev/null; then
  echo "Error: go not found in PATH"
  exit 1
fi
(cd "$(dirname "$0")/transfer" && GOMODCACHE="${TMPDIR:-/tmp}/gomod-transfer" make build)
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

RUN_ID=$(get_run_id "$BEFORE")
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

# Validate --json output non-interactively.
# Use --prefix json-test so this upload lands at a different object path and
# does not overwrite the object the interactive URL above points to.
info "Testing --json flag..."
JSON_OUT=$("$TRANSFER_BIN" upload \
  --workspace "$WORKSPACE" \
  --file "$TEST_FILE" \
  --prefix json-test \
  --expiry 30m \
  --json)
for field in url filename sha256 expires_at password; do
  val=$(echo "$JSON_OUT" | jq -r ".$field")
  if [[ -z "$val" || "$val" == "null" ]]; then
    echo "Error: --json output missing field '$field'"
    exit 1
  fi
done
ok "--json output is valid (url, filename, sha256, expires_at, password present)"

echo ""
ask "Open the URL printed above in a browser, enter the password, confirm the file downloads and contains the expected text — then press Enter"

# ---------------------------------------------------------------------------
# Step 4 — Pack folder (with --prefix to exercise subfolder routing)
# ---------------------------------------------------------------------------
step "4 / 5  Pack folder"

"$TRANSFER_BIN" pack \
  --workspace "$WORKSPACE" \
  --folder "$TEST_DIR" \
  --prefix test \
  --expiry 30m

echo ""
ask "Open the URL above in a browser, enter the password, verify the zip downloads and contains all 3 files with the subfolder — then press Enter"

# Verify the object was stored under the prefix.
info "Verifying object stored under prefix 'test/'..."
"$TRANSFER_BIN" list --workspace "$WORKSPACE" | grep "^test/" \
  || { echo "Error: no objects found under 'test/' prefix"; exit 1; }
ok "Object correctly stored under 'test/' prefix"

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

RUN_ID=$(get_run_id "$BEFORE")
wait_for_run "$RUN_ID"
ok "Workspace destroyed"

# ---------------------------------------------------------------------------
echo ""
echo "══════════════════════════════════════════════════════════════════════"
echo "  Test run complete. All steps passed."
echo "══════════════════════════════════════════════════════════════════════"
