#!/usr/bin/env python3
"""
Secure File Transfer — upload a file and generate a shareable signed URL.

Usage
-----
  python transfer.py upload --workspace acme-q1-report --file report.pdf --expiry 24h
  python transfer.py pack   --workspace acme-q1-report --folder ./documents

The workspace name is the only required identifier. The GCS bucket and signing
service account are derived automatically:
  bucket     = secure-transfer-<workspace>
  signing SA = st-signer-<workspace>@<project>.iam.gserviceaccount.com

The project ID is read from Application Default Credentials. No service-account
key file is created or needed.
"""

import argparse
import datetime
import hashlib
import pathlib
import re
import secrets
import string
import sys
import tempfile

import pyzipper

import google.auth
import google.auth.exceptions
import google.auth.transport.requests
import google.api_core.exceptions
from google.cloud import storage


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Same format enforced by transfer/workspace.go and .github/workflows/terraform.yml.
# If the allowed format changes, update all three locations.
_WORKSPACE_RE = re.compile(r'^[a-z0-9][a-z0-9-]{1,47}[a-z0-9]$')

def validate_workspace(name: str) -> None:
    """Exit with a clear message if the workspace name is not GCS-safe."""
    if not _WORKSPACE_RE.match(name):
        sys.exit(
            f"Error: invalid workspace name '{name}'.\n"
            "Workspace names must be 3–49 characters, lowercase letters, "
            "numbers, and hyphens only. Cannot start or end with a hyphen."
        )
    if '--' in name:
        sys.exit(f"Error: workspace name '{name}' cannot contain consecutive hyphens.")


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
    if td > datetime.timedelta(days=1):
        raise argparse.ArgumentTypeError(
            "Expiry cannot exceed 24 hours — files are auto-deleted after 1 day. Use a shorter expiry."
        )
    return td


def generate_password() -> str:
    """Return a cryptographically random 32-character alphanumeric password."""
    alphabet = string.ascii_letters + string.digits
    return ''.join(secrets.choice(alphabet) for _ in range(32))


def create_encrypted_zip(source: pathlib.Path, dest: pathlib.Path, password: str) -> None:
    """Create an AES-256 encrypted zip of source (file or folder) at dest."""
    with pyzipper.AESZipFile(
        dest, 'w',
        compression=pyzipper.ZIP_DEFLATED,
        encryption=pyzipper.WZ_AES,
    ) as zf:
        zf.setpassword(password.encode())
        if source.is_file():
            zf.write(source, source.name)
        else:
            for file in sorted(source.rglob('*')):
                if file.is_file():
                    zf.write(file, file.relative_to(source.parent))


def _sign_and_print(blob, object_name, sha256, expiry, signing_sa, source_credentials, password=None):
    """Generate a signed URL and print the shareable output block."""
    url = blob.generate_signed_url(
        version="v4",
        expiration=expiry,
        method="GET",
        service_account_email=signing_sa,
        access_token=source_credentials.token,
        response_disposition=f'attachment; filename="{object_name}"',
        response_type=blob.content_type,
    )

    expires_at = datetime.datetime.now(datetime.timezone.utc) + expiry
    print()
    print("=" * 72)
    print(f"Shareable URL (expires {expires_at.strftime('%Y-%m-%d %H:%M UTC')}):")
    print()
    print(url)
    print()
    print(f"Integrity:  SHA-256 = {sha256}")
    print("=" * 72)

    if password:
        print()
        print("─" * 72)
        print("PASSWORD — share via a separate channel, do NOT send with the URL:")
        print()
        print(password)
        print("─" * 72)


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

def cmd_upload(args):
    validate_workspace(args.workspace)
    filepath = pathlib.Path(args.file)
    if not filepath.exists():
        sys.exit(f"Error: file not found: {filepath}")

    password = generate_password()
    zip_name = f"{filepath.name}.zip"

    source_credentials, project = google.auth.default(
        scopes=["https://www.googleapis.com/auth/cloud-platform"]
    )
    source_credentials.refresh(google.auth.transport.requests.Request())

    bucket_name, signing_sa = resolve(args.workspace, project)
    client = storage.Client(credentials=source_credentials)

    object_name = f"{args.prefix.rstrip('/')}/{zip_name}" if args.prefix else zip_name
    blob = client.bucket(bucket_name).blob(object_name)

    with tempfile.TemporaryDirectory() as tmpdir:
        zip_path = pathlib.Path(tmpdir) / zip_name

        print(f"Encrypting  {filepath}  →  {zip_name}  (AES-256)")
        create_encrypted_zip(filepath, zip_path, password)

        sha256 = hashlib.sha256(zip_path.read_bytes()).hexdigest()

        print(f"Uploading  {zip_name}  →  gs://{bucket_name}/{object_name}")
        blob.upload_from_filename(str(zip_path), content_type="application/zip")
        print(f"Upload complete.  SHA-256: {sha256}")

        expiry = parse_expiry(args.expiry)
        _sign_and_print(blob, zip_name, sha256, expiry, signing_sa, source_credentials, password=password)


def cmd_pack(args):
    validate_workspace(args.workspace)
    folder = pathlib.Path(args.folder)
    if not folder.is_dir():
        sys.exit(f"Error: folder not found: {folder}")

    password = generate_password()
    zip_name = f"{folder.name}.zip"

    source_credentials, project = google.auth.default(
        scopes=["https://www.googleapis.com/auth/cloud-platform"]
    )
    source_credentials.refresh(google.auth.transport.requests.Request())

    bucket_name, signing_sa = resolve(args.workspace, project)
    client = storage.Client(credentials=source_credentials)

    with tempfile.TemporaryDirectory() as tmpdir:
        zip_path = pathlib.Path(tmpdir) / zip_name

        print(f"Packing    {folder}  →  {zip_name}  (AES-256)")
        create_encrypted_zip(folder, zip_path, password)

        sha256 = hashlib.sha256(zip_path.read_bytes()).hexdigest()

        blob = client.bucket(bucket_name).blob(zip_name)
        print(f"Uploading  {zip_name}  →  gs://{bucket_name}/{zip_name}")
        blob.upload_from_filename(str(zip_path), content_type="application/zip")
        print(f"Upload complete.  SHA-256: {sha256}")

        expiry = parse_expiry(args.expiry)
        _sign_and_print(blob, zip_name, sha256, expiry, signing_sa, source_credentials, password=password)


def cmd_list(args):
    validate_workspace(args.workspace)
    source_credentials, project = google.auth.default(
        scopes=["https://www.googleapis.com/auth/cloud-platform"]
    )
    bucket_name, _ = resolve(args.workspace, project)
    client = storage.Client(credentials=source_credentials)
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
    validate_workspace(args.workspace)
    if args.confirm != args.object:
        sys.exit(
            f"Error: confirmation does not match object name.\n"
            f"To delete '{args.object}', pass --confirm '{args.object}'"
        )
    source_credentials, project = google.auth.default(
        scopes=["https://www.googleapis.com/auth/cloud-platform"]
    )
    bucket_name, _ = resolve(args.workspace, project)
    client = storage.Client(credentials=source_credentials)
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
    p_upload.add_argument("--expiry", default="1h", help="URL lifetime: m/h/d (max 24h). Default: 1h")
    p_upload.add_argument("--prefix", default="", help="Optional folder prefix inside the bucket")

    p_pack = sub.add_parser("pack", parents=[common], help="Pack a folder into an AES-256 encrypted zip, upload, and print a signed URL and password")
    p_pack.add_argument("--folder", required=True, help="Local folder path to pack and upload")
    p_pack.add_argument("--expiry", default="1h", help="URL lifetime: m/h/d (max 24h). Default: 1h")

    p_list = sub.add_parser("list", parents=[common], help="List objects in the bucket")
    p_list.add_argument("--prefix", default="", help="Filter by prefix")

    p_del = sub.add_parser("delete", parents=[common], help="Delete an object from the bucket")
    p_del.add_argument("--object", required=True, help="Object name to delete")
    p_del.add_argument("--confirm", required=True, help="Repeat the object name to confirm deletion")

    return parser


def main():
    parser = build_parser()
    args = parser.parse_args()
    dispatch = {"upload": cmd_upload, "pack": cmd_pack, "list": cmd_list, "delete": cmd_delete}
    try:
        dispatch[args.command](args)
    except google.auth.exceptions.DefaultCredentialsError:
        sys.exit(
            "Error: no Application Default Credentials found.\n"
            "Run:  gcloud auth application-default login"
        )
    except google.auth.exceptions.TransportError:
        sys.exit(
            "Error: IAM signing request failed. This is usually an IAM propagation delay — "
            "wait a minute and retry, or verify your account is in GCP_SIGNING_MEMBERS."
        )
    except google.api_core.exceptions.NotFound:
        sys.exit("Error: resource not found. Check workspace name and that infrastructure has been provisioned.")
    except google.api_core.exceptions.PermissionDenied:
        sys.exit("Error: permission denied. Verify your account is listed in GCP_SIGNING_MEMBERS.")
    except google.api_core.exceptions.GoogleAPICallError as exc:
        sys.exit(f"Error: GCP API call failed ({type(exc).__name__}). Check GCP project and credentials.")
    except Exception as exc:  # noqa: BLE001
        sys.exit(f"Error: unexpected failure ({type(exc).__name__}).")


if __name__ == "__main__":
    main()
