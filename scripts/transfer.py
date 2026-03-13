#!/usr/bin/env python3
"""
Secure File Transfer — upload a file and generate a shareable signed URL.

Usage
-----
  python transfer.py upload --workspace acme-q1-report --file report.pdf --expiry 24h

The workspace name is the only required identifier. The GCS bucket and signing
service account are derived automatically:
  bucket     = secure-transfer-<workspace>
  signing SA = st-signer-<workspace>@<project>.iam.gserviceaccount.com

The project ID is read from Application Default Credentials. No service-account
key file is created or needed.
"""

import argparse
import datetime
import mimetypes
import pathlib
import sys

import google.auth
import google.auth.impersonated_credentials
import google.auth.transport.requests
from google.cloud import storage


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def resolve(workspace: str, project: str) -> tuple[str, str]:
    """Return (bucket_name, signing_sa_email) for a given workspace."""
    bucket = f"secure-transfer-{workspace}"
    signing_sa = f"st-signer-{workspace}@{project}.iam.gserviceaccount.com"
    return bucket, signing_sa


def parse_expiry(value: str) -> datetime.timedelta:
    units = {"m": "minutes", "h": "hours", "d": "days"}
    if not value or value[-1] not in units:
        raise argparse.ArgumentTypeError(
            f"Invalid expiry '{value}'. Use a number followed by m/h/d (e.g. 24h, 7d, 30m)."
        )
    try:
        amount = int(value[:-1])
    except ValueError:
        raise argparse.ArgumentTypeError(f"Invalid expiry '{value}'.")
    if amount <= 0:
        raise argparse.ArgumentTypeError("Expiry must be a positive number.")
    td = datetime.timedelta(**{units[value[-1]]: amount})
    if td > datetime.timedelta(days=7):
        raise argparse.ArgumentTypeError(
            "GCS V4 signed URLs cannot exceed 7 days. Use a shorter expiry."
        )
    return td


def adc_client(signing_sa: str) -> tuple[storage.Client, object]:
    """Authenticate via ADC and return an impersonated Storage client."""
    source_credentials, project = google.auth.default(
        scopes=["https://www.googleapis.com/auth/cloud-platform"]
    )
    source_credentials.refresh(google.auth.transport.requests.Request())
    target_creds = google.auth.impersonated_credentials.Credentials(
        source_credentials=source_credentials,
        target_principal=signing_sa,
        target_scopes=["https://www.googleapis.com/auth/cloud-platform"],
        lifetime=300,
    )
    return storage.Client(credentials=target_creds), project


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

def cmd_upload(args):
    filepath = pathlib.Path(args.file)
    if not filepath.exists():
        sys.exit(f"Error: file not found: {filepath}")

    source_credentials, project = google.auth.default(
        scopes=["https://www.googleapis.com/auth/cloud-platform"]
    )
    source_credentials.refresh(google.auth.transport.requests.Request())

    bucket_name, signing_sa = resolve(args.workspace, project)

    target_creds = google.auth.impersonated_credentials.Credentials(
        source_credentials=source_credentials,
        target_principal=signing_sa,
        target_scopes=["https://www.googleapis.com/auth/cloud-platform"],
        lifetime=300,
    )
    client = storage.Client(credentials=target_creds)

    object_name = (
        f"{args.prefix.rstrip('/')}/{filepath.name}" if args.prefix else filepath.name
    )
    blob = client.bucket(bucket_name).blob(object_name)

    content_type, _ = mimetypes.guess_type(str(filepath))
    content_type = content_type or "application/octet-stream"

    print(f"Uploading  {filepath}  →  gs://{bucket_name}/{object_name}")
    blob.upload_from_filename(str(filepath), content_type=content_type)
    print("Upload complete.")

    expiry = parse_expiry(args.expiry)
    url = blob.generate_signed_url(
        version="v4",
        expiration=expiry,
        method="GET",
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
    source_credentials, project = google.auth.default(
        scopes=["https://www.googleapis.com/auth/cloud-platform"]
    )
    source_credentials.refresh(google.auth.transport.requests.Request())
    bucket_name, signing_sa = resolve(args.workspace, project)
    target_creds = google.auth.impersonated_credentials.Credentials(
        source_credentials=source_credentials,
        target_principal=signing_sa,
        target_scopes=["https://www.googleapis.com/auth/cloud-platform"],
        lifetime=300,
    )
    client = storage.Client(credentials=target_creds)
    blobs = list(client.bucket(bucket_name).list_blobs(prefix=args.prefix or None))
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
    source_credentials, project = google.auth.default(
        scopes=["https://www.googleapis.com/auth/cloud-platform"]
    )
    source_credentials.refresh(google.auth.transport.requests.Request())
    bucket_name, signing_sa = resolve(args.workspace, project)
    target_creds = google.auth.impersonated_credentials.Credentials(
        source_credentials=source_credentials,
        target_principal=signing_sa,
        target_scopes=["https://www.googleapis.com/auth/cloud-platform"],
        lifetime=300,
    )
    client = storage.Client(credentials=target_creds)
    client.bucket(bucket_name).blob(args.object).delete()
    print(f"Deleted gs://{bucket_name}/{args.object}")


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument(
        "--workspace", required=True,
        help="Workspace name used in terraform apply (e.g. acme-q1-report)",
    )

    parser = argparse.ArgumentParser(
        description="Secure File Transfer — upload files and generate signed download URLs",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_upload = sub.add_parser("upload", parents=[common], help="Upload a file and print a signed URL")
    p_upload.add_argument("--file", required=True, help="Local file path to upload")
    p_upload.add_argument("--expiry", default="1h", help="URL lifetime: m/h/d (max 7d). Default: 1h")
    p_upload.add_argument("--prefix", default="", help="Optional folder prefix inside the bucket")

    p_list = sub.add_parser("list", parents=[common], help="List objects in the bucket")
    p_list.add_argument("--prefix", default="", help="Filter by prefix")

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
