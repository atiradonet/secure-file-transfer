output "bucket_name" {
  description = "Name of the private GCS bucket for this workspace (derived from --workspace by the CLI)"
  value       = google_storage_bucket.transfer.name
}

output "signing_sa_email" {
  description = "Signing service account email for this workspace (derived from --workspace by the CLI)"
  value       = google_service_account.signer.email
}
