terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

provider "google" {
  project = var.project_id
}

resource "random_id" "suffix" {
  byte_length = 4
}

# ---------------------------------------------------------------------------
# Private GCS bucket
# ---------------------------------------------------------------------------
resource "google_storage_bucket" "transfer" {
  name                        = "secure-transfer-${random_id.suffix.hex}"
  location                    = var.region
  force_destroy               = true          # allows terraform destroy to remove objects
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"    # never accidentally public

  lifecycle_rule {
    condition {
      age = var.file_retention_days
    }
    action {
      type = "Delete"
    }
  }
}

# ---------------------------------------------------------------------------
# Service account used exclusively for signing download URLs
# No long-lived key is created; callers impersonate it via the IAM API.
# ---------------------------------------------------------------------------
resource "google_service_account" "signer" {
  account_id   = "secure-transfer-signer"
  display_name = "Secure Transfer — URL Signer"
}

# The signing SA needs to read objects in order for a signed URL to be valid
resource "google_storage_bucket_iam_member" "signer_viewer" {
  bucket = google_storage_bucket.transfer.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.signer.email}"
}

# Allow listed humans/SAs to impersonate the signer SA so they can call
# iam.serviceAccounts.signBlob (used by the Python script for V4 signed URLs)
resource "google_service_account_iam_member" "token_creators" {
  for_each           = toset(var.signing_sa_members)
  service_account_id = google_service_account.signer.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = each.value
}

# The signing SA also needs to sign its own blobs (self-impersonation path)
resource "google_service_account_iam_member" "self_signer" {
  service_account_id = google_service_account.signer.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = "serviceAccount:${google_service_account.signer.email}"
}

# Allow listed members to upload objects to the bucket
resource "google_storage_bucket_iam_member" "uploaders" {
  for_each = toset(var.signing_sa_members)
  bucket   = google_storage_bucket.transfer.name
  role     = "roles/storage.objectCreator"
  member   = each.value
}
