output "bucket_name" {
  description = "Name of the private GCS bucket — pass to transfer.py --bucket"
  value       = google_storage_bucket.transfer.name
}

output "signing_sa_email" {
  description = "Signing service account email — pass to transfer.py --signing-sa"
  value       = google_service_account.signer.email
}
