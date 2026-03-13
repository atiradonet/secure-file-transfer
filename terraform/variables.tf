variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCS bucket location (multi-region or region, e.g. US, EU, us-central1)"
  type        = string
  default     = "US"
}

variable "file_retention_days" {
  description = "Days after which uploaded files are automatically deleted"
  type        = number
  default     = 7
}

variable "signing_sa_members" {
  description = "IAM members (e.g. user:you@example.com) allowed to upload files and generate signed URLs"
  type        = list(string)
}
