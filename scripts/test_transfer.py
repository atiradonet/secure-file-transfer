"""Unit tests for transfer.py"""

import datetime
import argparse
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
        assert transfer.parse_expiry("7d") == datetime.timedelta(days=7)

    def test_default_is_one_hour(self):
        parser = transfer.build_parser()
        args = parser.parse_args(["upload", "--workspace", "test", "--file", "x.pdf"])
        assert args.expiry == "1h"

    def test_exceeds_max_raises(self):
        with pytest.raises(argparse.ArgumentTypeError, match="7 days"):
            transfer.parse_expiry("8d")

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
            self.parser.parse_args(["upload", "--workspace", "test"])

    def test_upload_defaults(self):
        args = self.parser.parse_args(["upload", "--workspace", "ws", "--file", "f.pdf"])
        assert args.expiry == "1h"
        assert args.prefix == ""

    def test_delete_requires_object(self):
        with pytest.raises(SystemExit):
            self.parser.parse_args(["delete", "--workspace", "ws"])

    def test_list_parses(self):
        args = self.parser.parse_args(["list", "--workspace", "ws"])
        assert args.workspace == "ws"
        assert args.prefix == ""


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
    @patch("transfer.google.auth.impersonated_credentials.Credentials")
    @patch("transfer.storage.Client")
    def test_upload_calls_gcs_and_prints_url(
        self, mock_client_cls, mock_impersonated, mock_default, tmp_path, capsys
    ):
        # Arrange
        test_file = tmp_path / "report.pdf"
        test_file.write_bytes(b"data")

        mock_creds = MagicMock()
        mock_creds.token = "tok"
        mock_default.return_value = (mock_creds, "test-project")

        mock_blob = MagicMock()
        mock_blob.generate_signed_url.return_value = "https://signed.url/report.pdf"
        mock_bucket = MagicMock()
        mock_bucket.blob.return_value = mock_blob
        mock_client_cls.return_value.bucket.return_value = mock_bucket

        # Act
        transfer.cmd_upload(self._make_args(test_file))

        # Assert
        mock_blob.upload_from_filename.assert_called_once_with(
            str(test_file), content_type="application/pdf"
        )
        mock_blob.generate_signed_url.assert_called_once()
        out = capsys.readouterr().out
        assert "https://signed.url/report.pdf" in out

    @patch("transfer.google.auth.default")
    @patch("transfer.google.auth.impersonated_credentials.Credentials")
    @patch("transfer.storage.Client")
    def test_upload_uses_correct_bucket(
        self, mock_client_cls, mock_impersonated, mock_default, tmp_path
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
    @patch("transfer.google.auth.impersonated_credentials.Credentials")
    @patch("transfer.storage.Client")
    def test_prefix_prepended_to_object_name(
        self, mock_client_cls, mock_impersonated, mock_default, tmp_path
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

        mock_bucket.blob.assert_called_with("folder/file.txt")
