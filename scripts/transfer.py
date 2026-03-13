#!/usr/bin/env python3
"""
Secure File Transfer — upload a file and generate a shareable signed URL.

Usage
-----
  python transfer.py upload \\
      --file        report.pdf \\
      --bucket      secure-transfer-xxxx \\
      --signing-sa  secure-transfer-signer@project.iam.gserviceaccount.com \\
      --expiry      24h

The caller must be authenticated via Application Default Credentials and must
have roles/iam.serviceAccountTokenCreator on the signing service account
(granted by Terraform to the principals listed in signing_sa_members).

No service-account key file is created or needed.
"""

import argparse
import datetime
import mimetypes
import os
import pathlib
import sys

import google.auth
import google.auth.impersonated_credentials
import google.auth.transport.requests
from google.cloud import storage


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def parse_expiry(value: str) -> datetime.timedelta:
    """Parse a human-friendly duration like '24h', '7d', '30m' into a timedelta."""
    units = {"m": "minutes", "h": "hours", "d": "days"}
    if not value or not value[-1] in units:
        raise argparse.ArgumentTypeError(
            f"Invalid expiry '{value}'. Use a number followed by m/h/d (e.g. 24h, 7d, 30m)."
        )
    try:
        amount = int(value[:-1])
    except ValueError:
        raise argparse.ArgumentTypeError(f"Invalid expiry '{value}'.")
    if amount <= 0:
        raise argparse.ArgumentTypeError("Expiry must be a positive number.")
    # V4 signed URLs have a maximum lifetime of 7 days
    td = datetime.timedelta(**{units[value[-1]]: amount})
    if td > datetime.timedelta(days=7):
        raise argparse.ArgumentTypeError(
            "GCS V4 signed URLs cannot exceed 7 days. Use a shorter expiry."
        )
    return td


def impersonated_storage_client(
    signing_sa: str,
    source_credentials,
) -> storage.Client:
    """Return a Storage client whose requests are signed by *signing_sa*."""
    target_creds = google.auth.impersonated_credentials.Credentials(
        source_credentials=source_credentials,
        target_principal=signing_sa,
        target_scopes=["https://www.googleapis.com/auth/cloud-platform"],
        lifetime=300,
    )
    return storage.Client(credentials=target_creds)


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

def cmd_upload(args):
    filepath = pathlib.Path(args.file)
    if not filepath.exists():
        sys.exit(f"Error: file not found: {filepath}")

    # Authenticate with ADC (runs as the operator, not the signing SA)
    source_credentials, project = google.auth.default(
        scopes=["https://www.googleapis.com/auth/cloud-platform"]
    )
    source_credentials.refresh(google.auth.transport.requests.Request())

    # Build an impersonated client so the signing SA signs the URL blob
    client = impersonated_storage_client(args.signing_sa, source_credentials)

    bucket = client.bucket(args.bucket)

    # Preserve directory structure inside the bucket when --prefix is given
    object_name = (
        f"{args.prefix.rstrip('/')}/{filepath.name}"
        if args.prefix
        else filepath.name
    )

    blob = bucket.blob(object_name)

    # Detect content type so the browser knows how to handle the file
    content_type, _ = mimetypes.guess_type(str(filepath))
    content_type = content_type or "application/octet-stream"

    print(f"Uploading  {filepath}  →  gs://{args.bucket}/{object_name}")
    blob.upload_from_filename(str(filepath), content_type=content_type)
    print("Upload complete.")

    expiry = parse_expiry(args.expiry)

    url = blob.generate_signed_url(
        version="v4",
        expiration=expiry,
        method="GET",
        # Force the browser to download the file rather than display it inline
        response_disposition=f'attachment; filename="{filepath.name}"',
        response_type=content_type,
    )

    expires_at = datetime.datetime.now(datetime.timezone.utc) + expiry
    print()
    print("=" * 72)
    print(f"Shareable URL (expires {expires_at.strftime('%Y-%m-%d %H:%M UTC')}):")
    print()
    print(url)
    print("=" * 72)


def cmd_list(args):
    source_credentials, _ = google.auth.default(
        scopes=["https://www.googleapis.com/auth/cloud-platform"]
    )
    client = impersonated_storage_client(args.signing_sa, source_credentials)
    bucket = client.bucket(args.bucket)

    blobs = list(bucket.list_blobs(prefix=args.prefix or None))
    if not blobs:
        print("Bucket is empty.")
        return

    print(f"{'Object':60s}  {'Size':>12}  {'Updated'}")
    print("-" * 90)
    for b in blobs:
        size = f"{b.size:,}" if b.size is not None else "—"
        updated = b.updated.strftime("%Y-%m-%d %H:%M UTC") if b.updated else "—"
        print(f"{b.name:60s}  {size:>12}  {updated}")


def cmd_delete(args):
    source_credentials, _ = google.auth.default(
        scopes=["https://www.googleapis.com/auth/cloud-platform"]
    )
    client = impersonated_storage_client(args.signing_sa, source_credentials)
    bucket = client.bucket(args.bucket)
    blob = bucket.blob(args.object)
    blob.delete()
    print(f"Deleted gs://{args.bucket}/{args.object}")


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument(
        "--bucket", required=True,
        help="GCS bucket name (from Terraform output: bucket_name)",
    )
    common.add_argument(
        "--signing-sa", required=True, dest="signing_sa",
        help="Signing service account email (from Terraform output: signing_sa_email)",
    )

    parser = argparse.ArgumentParser(
        description="Secure File Transfer — upload files and generate signed download URLs",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    # upload
    p_upload = sub.add_parser("upload", parents=[common], help="Upload a file and print a signed URL")
    p_upload.add_argument("--file", required=True, help="Local file path to upload")
    p_upload.add_argument(
        "--expiry", default="24h",
        help="URL lifetime: number followed by m/h/d (max 7d). Default: 24h",
    )
    p_upload.add_argument("--prefix", default="", help="Optional folder prefix inside the bucket")

    # list
    p_list = sub.add_parser("list", parents=[common], help="List objects in the bucket")
    p_list.add_argument("--prefix", default="", help="Filter by prefix")

    # delete
    p_del = sub.add_parser("delete", parents=[common], help="Delete an object from the bucket")
    p_del.add_argument("--object", required=True, help="Object name to delete")

    return parser


def main():
    parser = build_parser()
    args = parser.parse_args()

    dispatch = {"upload": cmd_upload, "list": cmd_list, "delete": cmd_delete}
    try:
        dispatch[args.command](args)
    except google.auth.exceptions.DefaultCredentialsError:
        sys.exit(
            "Error: no Application Default Credentials found.\n"
            "Run:  gcloud auth application-default login"
        )
    except Exception as exc:  # noqa: BLE001
        sys.exit(f"Error: {exc}")


if __name__ == "__main__":
    main()
