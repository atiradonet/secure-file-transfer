"""Unit tests for transfer.py"""

import datetime
import argparse
import string
import zipfile
import pytest
from unittest.mock import MagicMock, patch, call
import sys
import os

sys.path.insert(0, os.path.dirname(__file__))
import transfer


# ---------------------------------------------------------------------------
# parse_expiry
# ---------------------------------------------------------------------------

class TestParseExpiry:
    def test_hours(self):
        assert transfer.parse_expiry("1h") == datetime.timedelta(hours=1)

    def test_minutes(self):
        assert transfer.parse_expiry("30m") == datetime.timedelta(minutes=30)

    def test_days(self):
        assert transfer.parse_expiry("1d") == datetime.timedelta(days=1)

    def test_default_is_one_hour(self):
        parser = transfer.build_parser()
        args = parser.parse_args(["upload", "--workspace", "test-ws", "--file", "x.pdf"])
        assert args.expiry == "1h"

    def test_exceeds_max_raises(self):
        with pytest.raises(argparse.ArgumentTypeError, match="24 hours"):
            transfer.parse_expiry("2d")

    def test_zero_raises(self):
        with pytest.raises(argparse.ArgumentTypeError, match="positive"):
            transfer.parse_expiry("0h")

    def test_negative_raises(self):
        with pytest.raises(argparse.ArgumentTypeError):
            transfer.parse_expiry("-1h")

    def test_missing_unit_raises(self):
        with pytest.raises(argparse.ArgumentTypeError):
            transfer.parse_expiry("24")

    def test_unknown_unit_raises(self):
        with pytest.raises(argparse.ArgumentTypeError):
            transfer.parse_expiry("2w")

    def test_non_numeric_raises(self):
        with pytest.raises(argparse.ArgumentTypeError):
            transfer.parse_expiry("abch")


# ---------------------------------------------------------------------------
# validate_workspace
# ---------------------------------------------------------------------------

class TestValidateWorkspace:
    def test_valid_name(self):
        transfer.validate_workspace("acme-q1-report")  # no exception

    def test_valid_alphanumeric(self):
        transfer.validate_workspace("abc123")  # no exception

    def test_too_short_raises(self):
        with pytest.raises(SystemExit):
            transfer.validate_workspace("ab")

    def test_leading_hyphen_raises(self):
        with pytest.raises(SystemExit):
            transfer.validate_workspace("-invalid")

    def test_trailing_hyphen_raises(self):
        with pytest.raises(SystemExit):
            transfer.validate_workspace("invalid-")

    def test_uppercase_raises(self):
        with pytest.raises(SystemExit):
            transfer.validate_workspace("Invalid-Name")

    def test_consecutive_hyphens_raises(self):
        with pytest.raises(SystemExit):
            transfer.validate_workspace("double--hyphen")

    def test_underscore_raises(self):
        with pytest.raises(SystemExit):
            transfer.validate_workspace("invalid_name")


# ---------------------------------------------------------------------------
# resolve
# ---------------------------------------------------------------------------

class TestResolve:
    def test_bucket_name(self):
        bucket, _ = transfer.resolve("acme-q1-report", "my-project")
        assert bucket == "secure-transfer-acme-q1-report"

    def test_signing_sa_email(self):
        _, sa = transfer.resolve("acme-q1-report", "my-project")
        assert sa == "st-signer-acme-q1-report@my-project.iam.gserviceaccount.com"

    def test_different_workspaces_give_different_buckets(self):
        bucket_a, _ = transfer.resolve("client-a", "proj")
        bucket_b, _ = transfer.resolve("client-b", "proj")
        assert bucket_a != bucket_b

    def test_same_workspace_different_projects_give_different_sas(self):
        _, sa_a = transfer.resolve("ws", "project-a")
        _, sa_b = transfer.resolve("ws", "project-b")
        assert sa_a != sa_b


# ---------------------------------------------------------------------------
# CLI argument parsing
# ---------------------------------------------------------------------------

class TestParser:
    def setup_method(self):
        self.parser = transfer.build_parser()

    def test_upload_requires_workspace(self):
        with pytest.raises(SystemExit):
            self.parser.parse_args(["upload", "--file", "report.pdf"])

    def test_upload_requires_file(self):
        with pytest.raises(SystemExit):
            self.parser.parse_args(["upload", "--workspace", "test-ws"])

    def test_upload_defaults(self):
        args = self.parser.parse_args(["upload", "--workspace", "my-ws", "--file", "f.pdf"])
        assert args.expiry == "1h"
        assert args.prefix == ""

    def test_delete_requires_object(self):
        with pytest.raises(SystemExit):
            self.parser.parse_args(["delete", "--workspace", "my-ws"])

    def test_delete_requires_confirm(self):
        with pytest.raises(SystemExit):
            self.parser.parse_args(["delete", "--workspace", "my-ws", "--object", "report.pdf"])

    def test_list_parses(self):
        args = self.parser.parse_args(["list", "--workspace", "my-ws"])
        assert args.workspace == "my-ws"
        assert args.prefix == ""

    def test_pack_requires_workspace(self):
        with pytest.raises(SystemExit):
            self.parser.parse_args(["pack", "--folder", "./docs"])

    def test_pack_requires_folder(self):
        with pytest.raises(SystemExit):
            self.parser.parse_args(["pack", "--workspace", "my-ws"])

    def test_pack_defaults(self):
        args = self.parser.parse_args(["pack", "--workspace", "my-ws", "--folder", "./docs"])
        assert args.expiry == "1h"


# ---------------------------------------------------------------------------
# cmd_upload (mocked GCP)
# ---------------------------------------------------------------------------

class TestCmdUpload:
    def _make_args(self, filepath, expiry="1h", prefix=""):
        args = MagicMock()
        args.workspace = "test-ws"
        args.file = str(filepath)
        args.expiry = expiry
        args.prefix = prefix
        return args

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_upload_calls_gcs_and_prints_url(
        self, mock_client_cls, mock_default, tmp_path, capsys
    ):
        # Arrange
        test_file = tmp_path / "report.pdf"
        test_file.write_bytes(b"data")

        mock_creds = MagicMock()
        mock_creds.token = "tok"
        mock_default.return_value = (mock_creds, "test-project")

        mock_blob = MagicMock()
        mock_blob.generate_signed_url.return_value = "https://signed.url/report.pdf.zip"
        mock_bucket = MagicMock()
        mock_bucket.blob.return_value = mock_blob
        mock_client_cls.return_value.bucket.return_value = mock_bucket

        # Act
        transfer.cmd_upload(self._make_args(test_file))

        # Assert — file is always uploaded as an AES-256 zip
        mock_blob.upload_from_filename.assert_called_once()
        _, kwargs = mock_blob.upload_from_filename.call_args
        assert kwargs["content_type"] == "application/zip"
        mock_blob.generate_signed_url.assert_called_once()
        out = capsys.readouterr().out
        assert "https://signed.url/report.pdf.zip" in out

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_upload_uses_correct_bucket(
        self, mock_client_cls, mock_default, tmp_path
    ):
        test_file = tmp_path / "doc.pdf"
        test_file.write_bytes(b"x")
        mock_creds = MagicMock()
        mock_default.return_value = (mock_creds, "my-project")
        mock_blob = MagicMock()
        mock_blob.generate_signed_url.return_value = "https://url"
        mock_client_cls.return_value.bucket.return_value.blob.return_value = mock_blob

        transfer.cmd_upload(self._make_args(test_file))

        mock_client_cls.return_value.bucket.assert_called_with("secure-transfer-test-ws")

    def test_upload_missing_file_exits(self, tmp_path):
        args = self._make_args(tmp_path / "nonexistent.pdf")
        with pytest.raises(SystemExit):
            transfer.cmd_upload(args)

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_upload_sha256_in_output(
        self, mock_client_cls, mock_default, tmp_path, capsys
    ):
        test_file = tmp_path / "file.txt"
        test_file.write_bytes(b"hello")
        mock_creds = MagicMock()
        mock_creds.token = "tok"
        mock_default.return_value = (mock_creds, "proj")
        mock_blob = MagicMock()
        mock_blob.generate_signed_url.return_value = "https://url"
        mock_client_cls.return_value.bucket.return_value.blob.return_value = mock_blob

        transfer.cmd_upload(self._make_args(test_file))

        assert "SHA-256" in capsys.readouterr().out

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_upload_prints_password(
        self, mock_client_cls, mock_default, tmp_path, capsys
    ):
        test_file = tmp_path / "report.pdf"
        test_file.write_bytes(b"data")
        mock_creds = MagicMock()
        mock_creds.token = "tok"
        mock_default.return_value = (mock_creds, "proj")
        mock_blob = MagicMock()
        mock_blob.generate_signed_url.return_value = "https://url"
        mock_client_cls.return_value.bucket.return_value.blob.return_value = mock_blob

        transfer.cmd_upload(self._make_args(test_file))

        assert "PASSWORD" in capsys.readouterr().out

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_upload_zip_name_matches_file(
        self, mock_client_cls, mock_default, tmp_path
    ):
        test_file = tmp_path / "report.pdf"
        test_file.write_bytes(b"data")
        mock_creds = MagicMock()
        mock_default.return_value = (mock_creds, "proj")
        mock_blob = MagicMock()
        mock_blob.generate_signed_url.return_value = "https://url"
        mock_bucket = MagicMock()
        mock_bucket.blob.return_value = mock_blob
        mock_client_cls.return_value.bucket.return_value = mock_bucket

        transfer.cmd_upload(self._make_args(test_file))

        mock_bucket.blob.assert_called_with("report.pdf.zip")

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_prefix_prepended_to_object_name(
        self, mock_client_cls, mock_default, tmp_path
    ):
        test_file = tmp_path / "file.txt"
        test_file.write_bytes(b"x")
        mock_creds = MagicMock()
        mock_default.return_value = (mock_creds, "proj")
        mock_blob = MagicMock()
        mock_blob.generate_signed_url.return_value = "https://url"
        mock_bucket = MagicMock()
        mock_bucket.blob.return_value = mock_blob
        mock_client_cls.return_value.bucket.return_value = mock_bucket

        transfer.cmd_upload(self._make_args(test_file, prefix="folder"))

        mock_bucket.blob.assert_called_with("folder/file.txt.zip")


# ---------------------------------------------------------------------------
# generate_password
# ---------------------------------------------------------------------------

class TestGeneratePassword:
    def test_length(self):
        assert len(transfer.generate_password()) == 32

    def test_alphanumeric_only(self):
        allowed = set(string.ascii_letters + string.digits)
        assert set(transfer.generate_password()) <= allowed

    def test_unique(self):
        assert transfer.generate_password() != transfer.generate_password()


# ---------------------------------------------------------------------------
# create_encrypted_zip
# ---------------------------------------------------------------------------

class TestCreateEncryptedZip:
    def test_zip_is_created(self, tmp_path):
        src = tmp_path / "docs"
        src.mkdir()
        (src / "file.txt").write_text("hello")
        dest = tmp_path / "out.zip"
        transfer.create_encrypted_zip(src, dest, "password123")
        assert dest.exists()

    def test_zip_contains_file(self, tmp_path):
        src = tmp_path / "docs"
        src.mkdir()
        (src / "report.pdf").write_bytes(b"data")
        dest = tmp_path / "out.zip"
        transfer.create_encrypted_zip(src, dest, "password123")
        import pyzipper
        with pyzipper.AESZipFile(dest) as zf:
            zf.setpassword(b"password123")
            names = zf.namelist()
        assert any("report.pdf" in n for n in names)

    def test_zip_preserves_structure(self, tmp_path):
        src = tmp_path / "docs"
        src.mkdir()
        sub = src / "invoices"
        sub.mkdir()
        (sub / "inv001.pdf").write_bytes(b"x")
        dest = tmp_path / "out.zip"
        transfer.create_encrypted_zip(src, dest, "password123")
        import pyzipper
        with pyzipper.AESZipFile(dest) as zf:
            zf.setpassword(b"password123")
            names = zf.namelist()
        assert any("invoices/inv001.pdf" in n for n in names)

    def test_single_file_is_zipped(self, tmp_path):
        src = tmp_path / "report.pdf"
        src.write_bytes(b"data")
        dest = tmp_path / "out.zip"
        transfer.create_encrypted_zip(src, dest, "password123")
        import pyzipper
        with pyzipper.AESZipFile(dest) as zf:
            zf.setpassword(b"password123")
            names = zf.namelist()
        assert names == ["report.pdf"]

    def test_wrong_password_raises(self, tmp_path):
        src = tmp_path / "docs"
        src.mkdir()
        (src / "file.txt").write_text("secret")
        dest = tmp_path / "out.zip"
        transfer.create_encrypted_zip(src, dest, "correctpassword")
        import pyzipper
        with pyzipper.AESZipFile(dest) as zf:
            zf.setpassword(b"wrongpassword")
            with pytest.raises(Exception):
                zf.read(zf.namelist()[0])


# ---------------------------------------------------------------------------
# cmd_pack (mocked GCP)
# ---------------------------------------------------------------------------

class TestCmdPack:
    def _make_args(self, folder, expiry="1h"):
        args = MagicMock()
        args.workspace = "test-ws"
        args.folder = str(folder)
        args.expiry = expiry
        return args

    def test_missing_folder_exits(self, tmp_path):
        args = self._make_args(tmp_path / "nonexistent")
        with pytest.raises(SystemExit):
            transfer.cmd_pack(args)

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_pack_uploads_zip_and_prints_url_and_password(
        self, mock_client_cls, mock_default, tmp_path, capsys
    ):
        src = tmp_path / "docs"
        src.mkdir()
        (src / "file.txt").write_text("hello")

        mock_creds = MagicMock()
        mock_creds.token = "tok"
        mock_default.return_value = (mock_creds, "proj")
        mock_blob = MagicMock()
        mock_blob.content_type = "application/zip"
        mock_blob.generate_signed_url.return_value = "https://signed.url/docs.zip"
        mock_client_cls.return_value.bucket.return_value.blob.return_value = mock_blob

        transfer.cmd_pack(self._make_args(src))

        mock_blob.upload_from_filename.assert_called_once()
        out = capsys.readouterr().out
        assert "https://signed.url/docs.zip" in out
        assert "PASSWORD" in out

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_zip_name_matches_folder(self, mock_client_cls, mock_default, tmp_path):
        src = tmp_path / "my-folder"
        src.mkdir()
        (src / "f.txt").write_text("x")

        mock_default.return_value = (MagicMock(), "proj")
        mock_blob = MagicMock()
        mock_blob.content_type = "application/zip"
        mock_blob.generate_signed_url.return_value = "https://url"
        mock_bucket = MagicMock()
        mock_bucket.blob.return_value = mock_blob
        mock_client_cls.return_value.bucket.return_value = mock_bucket

        transfer.cmd_pack(self._make_args(src))

        mock_bucket.blob.assert_called_with("my-folder.zip")

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_pack_sha256_in_output(
        self, mock_client_cls, mock_default, tmp_path, capsys
    ):
        src = tmp_path / "docs"
        src.mkdir()
        (src / "file.txt").write_text("hello")
        mock_creds = MagicMock()
        mock_creds.token = "tok"
        mock_default.return_value = (mock_creds, "proj")
        mock_blob = MagicMock()
        mock_blob.content_type = "application/zip"
        mock_blob.generate_signed_url.return_value = "https://url"
        mock_client_cls.return_value.bucket.return_value.blob.return_value = mock_blob

        transfer.cmd_pack(self._make_args(src))

        assert "SHA-256" in capsys.readouterr().out


# ---------------------------------------------------------------------------
# cmd_list (mocked GCP)
# ---------------------------------------------------------------------------

class TestCmdList:
    def _make_args(self, prefix=""):
        args = MagicMock()
        args.workspace = "test-ws"
        args.prefix = prefix
        return args

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_list_empty_bucket(self, mock_client_cls, mock_default, capsys):
        mock_default.return_value = (MagicMock(), "proj")
        mock_client_cls.return_value.bucket.return_value.list_blobs.return_value = []

        transfer.cmd_list(self._make_args())

        assert "empty" in capsys.readouterr().out.lower()

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_list_nonempty_bucket_prints_name_and_size(
        self, mock_client_cls, mock_default, capsys
    ):
        mock_default.return_value = (MagicMock(), "proj")
        blob = MagicMock()
        blob.name = "report.pdf"
        blob.size = 1024
        blob.updated = datetime.datetime(2026, 3, 1, 12, 0, tzinfo=datetime.timezone.utc)
        mock_client_cls.return_value.bucket.return_value.list_blobs.return_value = [blob]

        transfer.cmd_list(self._make_args())

        out = capsys.readouterr().out
        assert "report.pdf" in out
        assert "1,024" in out

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_list_blob_with_null_metadata_shows_dashes(
        self, mock_client_cls, mock_default, capsys
    ):
        mock_default.return_value = (MagicMock(), "proj")
        blob = MagicMock()
        blob.name = "file.txt"
        blob.size = None
        blob.updated = None
        mock_client_cls.return_value.bucket.return_value.list_blobs.return_value = [blob]

        transfer.cmd_list(self._make_args())

        out = capsys.readouterr().out
        assert "file.txt" in out
        assert "—" in out

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_list_prefix_forwarded(self, mock_client_cls, mock_default):
        mock_default.return_value = (MagicMock(), "proj")
        mock_bucket = MagicMock()
        mock_bucket.list_blobs.return_value = []
        mock_client_cls.return_value.bucket.return_value = mock_bucket

        transfer.cmd_list(self._make_args(prefix="invoices/"))

        mock_bucket.list_blobs.assert_called_with(prefix="invoices/")

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_list_uses_correct_bucket(self, mock_client_cls, mock_default):
        mock_default.return_value = (MagicMock(), "proj")
        mock_client_cls.return_value.bucket.return_value.list_blobs.return_value = []

        transfer.cmd_list(self._make_args())

        mock_client_cls.return_value.bucket.assert_called_with("secure-transfer-test-ws")


# ---------------------------------------------------------------------------
# cmd_delete (mocked GCP)
# ---------------------------------------------------------------------------

class TestCmdDelete:
    def _make_args(self, obj, confirm=None):
        args = MagicMock()
        args.workspace = "test-ws"
        args.object = obj
        args.confirm = confirm if confirm is not None else obj
        return args

    def test_mismatched_confirm_exits(self):
        args = self._make_args("report.pdf", confirm="wrong.pdf")
        with pytest.raises(SystemExit):
            transfer.cmd_delete(args)

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_delete_confirmed(self, mock_client_cls, mock_default, capsys):
        mock_default.return_value = (MagicMock(), "proj")
        mock_blob = MagicMock()
        mock_client_cls.return_value.bucket.return_value.blob.return_value = mock_blob

        transfer.cmd_delete(self._make_args("report.pdf"))

        mock_blob.delete.assert_called_once()
        assert "report.pdf" in capsys.readouterr().out

    @patch("transfer.google.auth.default")
    @patch("transfer.storage.Client")
    def test_delete_uses_correct_bucket(self, mock_client_cls, mock_default):
        mock_default.return_value = (MagicMock(), "proj")
        mock_client_cls.return_value.bucket.return_value.blob.return_value = MagicMock()

        transfer.cmd_delete(self._make_args("report.pdf"))

        mock_client_cls.return_value.bucket.assert_called_with("secure-transfer-test-ws")
