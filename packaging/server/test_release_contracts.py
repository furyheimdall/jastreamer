"""Release identity, signing, native evidence, and exact-artifact contracts."""

import hashlib
import importlib.util
import json
import pathlib
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import ci_artifact


def load_publisher():
    path = pathlib.Path(__file__).resolve().parent / "publish-release.py"
    spec = importlib.util.spec_from_file_location("publish_release", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


publish = load_publisher()
REVISION = "a" * 40
APK_BYTES = b"PK\x03\x04 signed release apk bytes"
UNSIGNED_BYTES = b"PK\x03\x04 unsigned ci apk bytes"


def write_signed_android(directory: pathlib.Path, **overrides):
    apk = directory / ci_artifact.ANDROID_RELEASE_APK
    apk.write_bytes(APK_BYTES)
    digest = hashlib.sha256(APK_BYTES).hexdigest()
    (directory / f"{apk.name}.sha256").write_text(f"{digest}  {apk.name}\n", encoding="ascii")
    manifest = {
        "schema": 1,
        "product": "jastreamer-android",
        "version": ci_artifact.VERSION,
        "versionName": ci_artifact.VERSION,
        "versionCode": ci_artifact.ANDROID_VERSION_CODE,
        "applicationId": ci_artifact.ANDROID_APPLICATION_ID,
        "minSdk": ci_artifact.ANDROID_MIN_SDK,
        "targetSdk": ci_artifact.ANDROID_TARGET_SDK,
        "sourceRevision": REVISION,
        "signed": True,
        "signatureSchemes": {"v1": False, "v2": True, "v3": True},
        "signerCertificateSha256": ci_artifact.release_certificate_sha256(),
        "apk": {"path": apk.name, "bytes": len(APK_BYTES), "sha256": digest},
        "ci": {
            "workflow": ".github/workflows/android.yml",
            "runId": 4242,
            "unsignedApk": {
                "path": ci_artifact.android_ci_apk_names(REVISION)[1],
                "bytes": len(UNSIGNED_BYTES),
                "sha256": hashlib.sha256(UNSIGNED_BYTES).hexdigest(),
            },
        },
    }
    manifest.update(overrides)
    (directory / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
    return manifest


def write_android_ci_artifact(directory: pathlib.Path, **overrides):
    debug_name, release_name = ci_artifact.android_ci_apk_names(REVISION)
    (directory / debug_name).write_bytes(b"debug apk")
    (directory / release_name).write_bytes(UNSIGNED_BYTES)
    (directory / "debug-manifest.xml").write_text("<manifest/>", encoding="utf-8")
    (directory / "release-manifest.xml").write_text("<manifest/>", encoding="utf-8")
    ci_artifact.write_sums(directory, [debug_name, release_name])
    provenance = {
        "sourceRevision": REVISION,
        "version": ci_artifact.VERSION,
        "productionQualified": False,
        "artifacts": [
            {
                "file": debug_name,
                "sha256": hashlib.sha256(b"debug apk").hexdigest(),
                "size": len(b"debug apk"),
                "signing": "Android SDK generated debug/test certificate; not for release",
                "certificateSha256": "A" * 64,
            },
            {
                "file": release_name,
                "sha256": hashlib.sha256(UNSIGNED_BYTES).hexdigest(),
                "size": len(UNSIGNED_BYTES),
                "signing": "unsigned; not installable until explicitly signed; not a production release",
            },
        ],
    }
    provenance.update(overrides)
    (directory / "provenance.json").write_text(json.dumps(provenance), encoding="utf-8")
    return provenance


class ReleaseChannelGrammarTests(unittest.TestCase):
    def test_plain_version_tag_selects_the_stable_channel(self):
        identity = publish.validate_identity(REVISION, "v0.2.2", "0.2.2")
        self.assertEqual(identity["channel"], "stable")
        self.assertFalse(identity["prerelease"])
        self.assertTrue(identity["latest"])
        self.assertIsNone(identity["previewNumber"])

    def test_preview_tag_selects_the_preview_channel(self):
        identity = publish.validate_identity(REVISION, "v0.2.2-preview.14", "0.2.2-preview.14")
        self.assertEqual(identity["channel"], "preview")
        self.assertTrue(identity["prerelease"])
        self.assertFalse(identity["latest"])
        self.assertEqual(identity["previewNumber"], 14)

    def test_release_and_image_tags_must_share_one_channel(self):
        for release_tag, image_tag in (
            ("v0.2.2", "0.2.2-preview.1"),
            ("v0.2.2-preview.1", "0.2.2"),
            ("v0.2.2-preview.2", "0.2.2-preview.3"),
        ):
            with self.subTest(release_tag=release_tag, image_tag=image_tag):
                with self.assertRaises(SystemExit) as raised:
                    publish.validate_identity(REVISION, release_tag, image_tag)
                self.assertIn("different channels", str(raised.exception))

    def test_tags_outside_the_grammar_are_rejected(self):
        for release_tag, image_tag in (
            ("0.2.2", "0.2.2"),
            ("v0.2.2", "v0.2.2"),
            ("latest", "latest"),
            ("v0.2.2", "latest"),
            ("v0.2.2-preview.0", "0.2.2-preview.0"),
            ("v0.2.2-rc.1", "0.2.2-rc.1"),
            ("v0.2.2-preview.1-preview.2", "0.2.2-preview.1-preview.2"),
            ("v0.2.2 ", "0.2.2"),
        ):
            with self.subTest(release_tag=release_tag, image_tag=image_tag):
                with self.assertRaises(SystemExit):
                    publish.validate_identity(REVISION, release_tag, image_tag)

    def test_tag_version_must_equal_the_product_version(self):
        with self.assertRaises(SystemExit) as raised:
            publish.validate_identity(REVISION, "v0.3.0", "0.3.0")
        self.assertIn("product version", str(raised.exception))

    def test_source_revision_must_be_a_full_lowercase_sha(self):
        for revision in ("A" * 40, "a" * 39, "", "abc"):
            with self.subTest(revision=revision):
                with self.assertRaises(SystemExit):
                    publish.validate_identity(revision, "v0.2.2", "0.2.2")


class SignedAndroidManifestTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.directory = pathlib.Path(self.temporary.name)
        self.addCleanup(self.temporary.cleanup)

    def test_signed_artifact_with_the_pinned_certificate_is_accepted(self):
        expected = write_signed_android(self.directory)
        manifest = ci_artifact.verify_android_release_artifact(self.directory, REVISION)
        self.assertEqual(manifest["apk"], expected["apk"])
        self.assertEqual(manifest["signerCertificateSha256"], ci_artifact.release_certificate_sha256())

    def test_pinned_certificate_file_holds_the_release_signer_digest(self):
        self.assertEqual(
            ci_artifact.release_certificate_sha256(),
            "53285C2C239AFF2927EBE6F5C6AEBB82FDBB50956ED84B1E9F0222B2D925943E",
        )

    def test_a_different_signer_certificate_is_rejected(self):
        write_signed_android(self.directory, signerCertificateSha256="B" * 64)
        with self.assertRaises(SystemExit) as raised:
            ci_artifact.verify_android_release_artifact(self.directory, REVISION)
        self.assertIn("pinned release key", str(raised.exception))

    def test_lowercase_certificate_digests_are_rejected(self):
        write_signed_android(self.directory, signerCertificateSha256=ci_artifact.release_certificate_sha256().lower())
        with self.assertRaises(SystemExit) as raised:
            ci_artifact.verify_android_release_artifact(self.directory, REVISION)
        self.assertIn("malformed", str(raised.exception))

    def test_unsigned_or_v1_only_signatures_are_rejected(self):
        for overrides in (
            {"signed": False},
            {"signatureSchemes": {"v1": True, "v2": False, "v3": False}},
            {"signatureSchemes": {"v1": False, "v2": True, "v3": False}},
            {"signatureSchemes": {"v1": True, "v2": True, "v3": True}},
        ):
            with self.subTest(overrides=overrides):
                write_signed_android(self.directory, **overrides)
                with self.assertRaises(SystemExit):
                    ci_artifact.verify_android_release_artifact(self.directory, REVISION)

    def test_application_identity_must_match_the_shipped_app(self):
        for overrides in (
            {"applicationId": "io.jastreamer.android.debug"},
            {"versionCode": 19999},
            {"versionName": "0.1.9"},
            {"minSdk": 21},
            {"targetSdk": 35},
        ):
            with self.subTest(overrides=overrides):
                write_signed_android(self.directory, **overrides)
                with self.assertRaises(SystemExit):
                    ci_artifact.verify_android_release_artifact(self.directory, REVISION)

    def test_a_foreign_source_revision_is_rejected(self):
        write_signed_android(self.directory, sourceRevision="b" * 40)
        with self.assertRaises(SystemExit) as raised:
            ci_artifact.verify_android_release_artifact(self.directory, REVISION)
        self.assertIn("source revision", str(raised.exception))

    def test_apk_bytes_must_match_the_recorded_digest_and_size(self):
        write_signed_android(self.directory)
        (self.directory / ci_artifact.ANDROID_RELEASE_APK).write_bytes(APK_BYTES + b"tampered")
        with self.assertRaises(SystemExit) as raised:
            ci_artifact.verify_android_release_artifact(self.directory, REVISION)
        self.assertIn("size mismatch", str(raised.exception))

    def test_checksum_sidecar_must_match_the_manifest(self):
        manifest = write_signed_android(self.directory)
        sidecar = self.directory / f"{ci_artifact.ANDROID_RELEASE_APK}.sha256"
        sidecar.write_text(f"{'0' * 64}  {manifest['apk']['path']}\n", encoding="ascii")
        with self.assertRaises(SystemExit) as raised:
            ci_artifact.verify_android_release_artifact(self.directory, REVISION)
        self.assertIn("sidecar", str(raised.exception))

    def test_extra_files_are_rejected_so_only_the_apk_is_published(self):
        write_signed_android(self.directory)
        (self.directory / "release.p12").write_bytes(b"key material")
        with self.assertRaises(SystemExit) as raised:
            ci_artifact.verify_android_release_artifact(self.directory, REVISION)
        self.assertIn("unexpected files", str(raised.exception))

    def test_the_signed_apk_must_name_its_android_ci_origin(self):
        for overrides in (
            {"ci": {"workflow": ".github/workflows/android.yml", "runId": 4242}},
            {"ci": {"runId": 0, "unsignedApk": {"path": ci_artifact.android_ci_apk_names(REVISION)[1], "bytes": 3, "sha256": "0" * 64}}},
            {"ci": {"runId": 4242, "unsignedApk": {"path": "jastreamer-android_0.2.2_release-unsigned.apk", "bytes": 3, "sha256": "0" * 64}}},
        ):
            with self.subTest(overrides=overrides):
                write_signed_android(self.directory, **overrides)
                with self.assertRaises(SystemExit):
                    ci_artifact.verify_android_release_artifact(self.directory, REVISION)


class AndroidCiArtifactTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.directory = pathlib.Path(self.temporary.name)
        self.addCleanup(self.temporary.cleanup)

    def test_unsigned_ci_release_apk_is_accepted_with_matching_provenance(self):
        write_android_ci_artifact(self.directory)
        result = ci_artifact.verify_android_ci_artifact(self.directory, REVISION)
        self.assertEqual(result["apk"], ci_artifact.android_ci_apk_names(REVISION)[1])
        self.assertEqual(result["sha256"], hashlib.sha256(UNSIGNED_BYTES).hexdigest())

    def test_a_release_apk_recorded_as_signed_is_rejected(self):
        provenance = write_android_ci_artifact(self.directory)
        provenance["artifacts"][1]["signing"] = "signed with the release key"
        (self.directory / "provenance.json").write_text(json.dumps(provenance), encoding="utf-8")
        with self.assertRaises(SystemExit) as raised:
            ci_artifact.verify_android_ci_artifact(self.directory, REVISION)
        self.assertIn("unsigned", str(raised.exception))

    def test_a_release_certificate_in_ci_provenance_is_rejected(self):
        provenance = write_android_ci_artifact(self.directory)
        provenance["artifacts"][1]["certificateSha256"] = "C" * 64
        (self.directory / "provenance.json").write_text(json.dumps(provenance), encoding="utf-8")
        with self.assertRaises(SystemExit) as raised:
            ci_artifact.verify_android_ci_artifact(self.directory, REVISION)
        self.assertIn("certificate", str(raised.exception))

    def test_ci_provenance_must_describe_the_selected_source_revision(self):
        write_android_ci_artifact(self.directory)
        with self.assertRaises(SystemExit):
            ci_artifact.verify_android_ci_artifact(self.directory, "b" * 40)

    def test_rebuilt_apk_bytes_are_rejected(self):
        write_android_ci_artifact(self.directory)
        (self.directory / ci_artifact.android_ci_apk_names(REVISION)[1]).write_bytes(UNSIGNED_BYTES + b"x")
        with self.assertRaises(SystemExit):
            ci_artifact.verify_android_ci_artifact(self.directory, REVISION)


class PublicReleaseChannelTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temporary.name)
        self.addCleanup(self.temporary.cleanup)
        self.staged = self.root / "staged"
        self.staged.mkdir()
        self.names = [ci_artifact.ANDROID_RELEASE_APK, ci_artifact.DESKTOP_TARGETS["macos-arm64"]["archive"]]
        for name in self.names:
            (self.staged / name).write_bytes(name.encode())

    def release_json(self, tag="v0.2.2", **overrides):
        release = {
            "tag_name": tag, "target_commitish": REVISION, "draft": False,
            "prerelease": False, "id": 77,
            "url": f"https://api.github.com/repos/{publish.REPOSITORY}/releases/77",
            "assets": [
                {"name": name, "state": "uploaded", "size": len(name),
                 "browser_download_url": f"https://github.com/{publish.REPOSITORY}/releases/download/{tag}/{name}"}
                for name in self.names
            ],
        }
        release.update(overrides)
        path = self.root / "release.json"
        path.write_text(json.dumps(release), encoding="utf-8")
        return path

    def latest_json(self, **overrides):
        latest = {"id": 77, "tag_name": "v0.2.2"}
        latest.update(overrides)
        path = self.root / "latest.json"
        path.write_text(json.dumps(latest), encoding="utf-8")
        return path

    def arguments(self, release_json, latest_json, release_tag="v0.2.2", image_tag="0.2.2"):
        return publish.argparse.Namespace(
            release_json=str(release_json), staged=str(self.staged), source_revision=REVISION,
            release_tag=release_tag, image_tag=image_tag,
            latest_json=str(latest_json) if latest_json else None,
        )

    def downloaded(self, url):
        payload = url.rsplit("/", 1)[1].encode()
        return len(payload), hashlib.sha256(payload).hexdigest()

    def test_stable_release_requires_the_anonymous_latest_tag(self):
        with patch.object(publish, "hash_public_url", side_effect=self.downloaded):
            publish.verify_public_release(self.arguments(self.release_json(), self.latest_json()))

    def test_stable_release_marked_prerelease_is_rejected(self):
        with self.assertRaises(SystemExit):
            publish.verify_public_release(self.arguments(self.release_json(prerelease=True), self.latest_json()))

    def test_stable_release_that_is_not_latest_is_rejected(self):
        with self.assertRaises(SystemExit):
            publish.verify_public_release(self.arguments(self.release_json(), self.latest_json(id=12, tag_name="v0.1.9")))

    def test_stable_release_without_an_anonymous_latest_response_is_rejected(self):
        with self.assertRaises(SystemExit):
            publish.verify_public_release(self.arguments(self.release_json(), None))

    def test_preview_release_must_be_a_prerelease_that_is_not_latest(self):
        tag = "v0.2.2-preview.3"
        arguments = self.arguments(self.release_json(tag=tag, prerelease=True), self.latest_json(id=12, tag_name="v0.1.9"), tag, "0.2.2-preview.3")
        with patch.object(publish, "hash_public_url", side_effect=self.downloaded):
            publish.verify_public_release(arguments)
        arguments.latest_json = str(self.latest_json(id=77, tag_name=tag))
        with self.assertRaises(SystemExit):
            publish.verify_public_release(arguments)

    def test_required_mobile_and_mac_downloads_cannot_be_omitted_even_from_staging(self):
        for missing in list(self.names):
            with self.subTest(missing=missing):
                self.names.remove(missing)
                (self.staged / missing).unlink()
                with patch.object(publish, "hash_public_url", side_effect=self.downloaded):
                    with self.assertRaises(SystemExit):
                        publish.verify_public_release(self.arguments(self.release_json(), self.latest_json()))
                self.names.append(missing)
                (self.staged / missing).write_bytes(missing.encode())

    def test_public_mac_download_must_match_the_staged_ci_bytes(self):
        mac = ci_artifact.DESKTOP_TARGETS["macos-arm64"]["archive"]
        def tampered(url):
            size, digest = self.downloaded(url)
            return (size, "0" * 64) if url.endswith(mac) else (size, digest)
        with patch.object(publish, "hash_public_url", side_effect=tampered):
            with self.assertRaises(SystemExit):
                publish.verify_public_release(self.arguments(self.release_json(), self.latest_json()))




class ReleaseNotesTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temporary.name)
        self.addCleanup(self.temporary.cleanup)
        self.staged = self.root / "staged"
        self.staged.mkdir()

    def stage(self, release_tag, image_tag):
        publication = {
            "registry": f"ghcr.io/{publish.REGISTRY_REPOSITORY}",
            "index": {"digest": "sha256:" + "1" * 64},
            "images": [
                {"platform": "linux/amd64", "manifestDigest": "sha256:" + "2" * 64},
                {"platform": "linux/arm64", "manifestDigest": "sha256:" + "3" * 64},
            ],
        }
        publication_name = f"jastreamer-server_{image_tag}.manifest.json"
        (self.staged / publication_name).write_text(json.dumps(publication), encoding="utf-8")
        provenance = {
            "channel": "preview" if "preview" in release_tag else "stable",
            "releaseTag": release_tag,
            "imageTag": image_tag,
            "sourceRevision": REVISION,
            "ciRunId": 111,
            "androidRunId": 222,
            "serverPublicationManifest": {"path": publication_name},
            "android": {
                "apk": {"path": ci_artifact.ANDROID_RELEASE_APK},
                "signerCertificateSha256": ci_artifact.release_certificate_sha256(),
            },
        }
        (self.staged / "release-provenance.json").write_text(json.dumps(provenance), encoding="utf-8")
        (self.staged / ci_artifact.ANDROID_RELEASE_APK).write_bytes(APK_BYTES)
        (self.staged / "SHA256SUMS").write_text("", encoding="ascii")
        notes = self.root / "notes.md"
        publish.render_notes(publish.argparse.Namespace(staged=str(self.staged), output=str(notes)))
        return notes.read_text(encoding="utf-8")

    def test_notes_reject_a_staged_channel_that_contradicts_the_tag(self):
        self.stage("v0.2.2", "0.2.2")
        provenance = json.loads((self.staged / "release-provenance.json").read_text(encoding="utf-8"))
        provenance["channel"] = "preview"
        (self.staged / "release-provenance.json").write_text(json.dumps(provenance), encoding="utf-8")
        with self.assertRaises(SystemExit) as raised:
            publish.render_notes(publish.argparse.Namespace(staged=str(self.staged), output=str(self.root / "notes.md")))
        self.assertIn("channel mismatch", str(raised.exception))




class ServerRunContractTest(unittest.TestCase):
    def run_validation(self, artifact_names, mac_job=None):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            run_id = 42
            payloads = {
                "run": {
                    "id": run_id, "name": publish.CI_NAME, "path": publish.CI_WORKFLOW, "workflow_id": 7,
                    "repository": {"full_name": publish.REPOSITORY}, "head_repository": {"full_name": publish.REPOSITORY},
                    "event": "push", "head_branch": "main", "status": "completed", "conclusion": "success",
                    "head_sha": REVISION, "head_commit": {"id": REVISION},
                },
                "workflow": {"id": 7, "path": publish.CI_WORKFLOW, "name": publish.CI_NAME, "state": "active"},
                "branch": {"name": "main", "protected": True},
                "comparison": {"status": "identical", "base_commit": {"sha": REVISION}},
                "jobs": {"total_count": len(publish.EXPECTED_JOBS), "jobs": [
                    {"name": name, "status": "completed", "conclusion": "success", "head_sha": REVISION}
                    for name in sorted(publish.EXPECTED_JOBS)
                ]},
                "artifacts": {"total_count": len(artifact_names), "artifacts": [
                    {"name": name, "expired": False, "id": index + 1, "size_in_bytes": 10,
                     "digest": "sha256:" + "b" * 64, "workflow_run": {"id": run_id, "head_sha": REVISION}}
                    for index, name in enumerate(sorted(artifact_names))
                ]},
            }
            if mac_job is not None:
                job = next(job for job in payloads["jobs"]["jobs"] if job["name"] == "macOS arm64 ad-hoc desktop")
                job.update(mac_job)
            for name, value in payloads.items():
                (root / f"{name}.json").write_text(json.dumps(value), encoding="utf-8")
            output = root / "provenance.json"
            args = type("Args", (), {
                "repository": publish.REPOSITORY, "run_id": str(run_id), "kind": "server", "expect_source_revision": "",
                "run": root / "run.json", "workflow": root / "workflow.json", "branch": root / "branch.json",
                "comparison": root / "comparison.json", "jobs": root / "jobs.json", "artifacts": root / "artifacts.json",
                "output": output, "github_output": "",
            })()
            with patch("builtins.print"):
                publish.validate_run(args)
            return json.loads(output.read_text(encoding="utf-8"))

    def test_evidence_artifact_is_required_but_never_published(self):
        provenance = self.run_validation(publish.EXPECTED_ARTIFACTS | {"windows-audio-settings"})
        self.assertEqual({record["name"] for record in provenance["artifacts"]}, publish.EXPECTED_ARTIFACTS)

    def test_missing_evidence_artifact_is_rejected(self):
        with self.assertRaises(SystemExit):
            self.run_validation(set(publish.EXPECTED_ARTIFACTS))

    def test_missing_mac_artifact_is_rejected(self):
        with self.assertRaises(SystemExit):
            self.run_validation((publish.EXPECTED_ARTIFACTS | publish.SERVER_EVIDENCE_ARTIFACTS) - {"jastreamer-desktop-macos-arm64"})

    def test_skipped_failed_or_wrong_revision_mac_job_blocks_publication(self):
        for overrides in ({"conclusion": "skipped"}, {"conclusion": "failure"}, {"head_sha": "b" * 40}):
            with self.subTest(overrides=overrides):
                with self.assertRaises(SystemExit):
                    self.run_validation(publish.EXPECTED_ARTIFACTS | publish.SERVER_EVIDENCE_ARTIFACTS, overrides)


class MacArtifactEvidenceTests(unittest.TestCase):
    """Synthetic package bytes exercise receipt validation, not runtime qualification."""

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = pathlib.Path(self.temporary.name)
        archive = self.directory / ci_artifact.DESKTOP_TARGETS["macos-arm64"]["archive"]
        archive.write_bytes(b"contract fixture DMG bytes")
        runtime = ["jastreamer-audio", "libavcodec.62.dylib", "libavformat.62.dylib", "libavutil.60.dylib", "libswresample.6.dylib"]
        names = ["Contents/MacOS/jastreamer-desktop", *(f"Contents/Resources/native-audio/{name}" for name in runtime)]
        names.extend(f"Contents/Frameworks/fixture-{index}" for index in range(12))
        self.manifest = {
            "product": "jastreamer-desktop", "version": ci_artifact.VERSION,
            "platform": "darwin", "arch": "arm64", "electron": "44.3.0",
            "sourceRevision": REVISION, "distribution": "public-adhoc",
            "developmentBuild": False, "signed": False, "signing": "ad-hoc",
            "developerIDSigned": False, "notarized": False, "productionQualified": False,
            "minimumMacOS": "13.0", "directory": "jastreamer.app",
            "archive": {"path": archive.name, "bytes": archive.stat().st_size, "sha256": ci_artifact.sha256_file(archive)},
            "nativeAudio": {"helper": "Contents/Resources/native-audio/jastreamer-audio", "runtime": runtime, "ffmpeg": "8.1.2", "dynamicLinking": True},
            "files": [
                {"path": name, "type": "file", "mode": 0o755, "bytes": len(name), "sha256": hashlib.sha256(name.encode()).hexdigest()}
                for name in names
            ],
        }
        (self.directory / f"{archive.name}.sha256").write_text(f"{self.manifest['archive']['sha256']}  {archive.name}\n", encoding="ascii")
        self.native = {
            key: self.manifest[key] for key in ("product", "version", "platform", "arch", "sourceRevision", "distribution", "signing", "developerIDSigned", "notarized", "productionQualified", "archive")
        }
        self.native.update({
            "schema": 1,
            "nativeTests": ["coreaudio-format", "coreaudio-pcm", "decoder", "packing", "protocol"],
            "checks": ci_artifact.DESKTOP_TARGETS["macos-arm64"]["checks"][:-1],
            "machOFiles": [{"path": entry["path"], "arch": "arm64", "sha256": entry["sha256"]} for entry in self.manifest["files"]],
        })
        self.launch = {
            key: self.manifest[key] for key in ("product", "version", "platform", "arch", "sourceRevision", "distribution", "archive")
        }
        self.launch.update({
            "schema": 1, "checks": list(ci_artifact.MACOS_LAUNCH_CHECKS),
            "appExecutable": {"path": names[0], "sha256": self.manifest["files"][0]["sha256"]},
            "helper": {"pid": 42, "exited": True},
        })
        self.save_evidence()
        with patch("builtins.print"):
            ci_artifact.create_desktop(ci_artifact.argparse.Namespace(dist=self.directory, target="macos-arm64", source_revision=REVISION))

    def save_evidence(self):
        ci_artifact.canonical_json(self.directory / "manifest.json", self.manifest)
        manifest = self.directory / "manifest.json"
        self.native["packageManifest"] = {"path": manifest.name, "bytes": manifest.stat().st_size, "sha256": ci_artifact.sha256_file(manifest)}
        self.launch["packageManifest"] = dict(self.native["packageManifest"])
        ci_artifact.canonical_json(self.directory / "native-verification.json", self.native)
        ci_artifact.canonical_json(self.directory / "launch-verification.json", self.launch)

    def verify(self):
        return ci_artifact.verify_desktop_artifact(self.directory, "macos-arm64", REVISION)

    def test_complete_evidence_binds_archive_and_both_native_receipts(self):
        receipt = self.verify()
        self.assertEqual(receipt["archive"]["sha256"], ci_artifact.sha256_file(self.directory / self.manifest["archive"]["path"]))
        for field, name in (("nativeVerification", "native-verification.json"), ("launchVerification", "launch-verification.json")):
            self.assertEqual(receipt[field]["sha256"], ci_artifact.sha256_file(self.directory / name))

    def test_missing_native_or_launch_evidence_is_rejected(self):
        for name in ("native-verification.json", "launch-verification.json"):
            with self.subTest(name=name):
                evidence = self.directory / name
                original = evidence.read_bytes()
                evidence.unlink()
                with self.assertRaises(SystemExit):
                    self.verify()
                evidence.write_bytes(original)

    def test_tampered_dmg_is_rejected(self):
        archive = self.directory / self.manifest["archive"]["path"]
        archive.write_bytes(b"x" * archive.stat().st_size)
        with self.assertRaises(SystemExit):
            self.verify()

    def test_wrong_architecture_revision_or_qualification_cannot_create_a_receipt(self):
        for field, bad in (("arch", "x64"), ("sourceRevision", "b" * 40), ("distribution", "local-development"), ("developmentBuild", True), ("developerIDSigned", True), ("notarized", True), ("productionQualified", True)):
            with self.subTest(field=field):
                original = self.manifest[field]
                self.manifest[field] = bad
                self.save_evidence()
                with self.assertRaises(SystemExit):
                    ci_artifact.create_desktop(ci_artifact.argparse.Namespace(dist=self.directory, target="macos-arm64", source_revision=REVISION))
                self.manifest[field] = original

    def test_native_test_and_macho_evidence_cannot_be_forged_by_general_receipt_creation(self):
        cases = [
            ("nativeTests", []), ("machOFiles", self.native["machOFiles"][:-1]),
            ("machOFiles", [{**self.native["machOFiles"][0], "arch": "x86_64"}, *self.native["machOFiles"][1:]]),
            ("machOFiles", [{**self.native["machOFiles"][0], "sha256": "0" * 64}, *self.native["machOFiles"][1:]]),
            ("sourceRevision", "b" * 40), ("checks", []),
        ]
        for field, bad in cases:
            with self.subTest(field=field, bad=bad):
                original = self.native[field]
                self.native[field] = bad
                self.save_evidence()
                with self.assertRaises(SystemExit):
                    ci_artifact.create_desktop(ci_artifact.argparse.Namespace(dist=self.directory, target="macos-arm64", source_revision=REVISION))
                self.native[field] = original

    def test_skipped_launch_checks_wrong_binary_or_live_helper_are_rejected(self):
        for field, bad in (("checks", self.launch["checks"][:-1]), ("sourceRevision", "b" * 40), ("appExecutable", {"path": "Contents/MacOS/jastreamer-desktop", "sha256": "0" * 64}), ("helper", {"pid": 42, "exited": False})):
            with self.subTest(field=field):
                original = self.launch[field]
                self.launch[field] = bad
                self.save_evidence()
                with self.assertRaises(SystemExit):
                    ci_artifact.create_desktop(ci_artifact.argparse.Namespace(dist=self.directory, target="macos-arm64", source_revision=REVISION))
                self.launch[field] = original

    def test_rewriting_valid_evidence_requires_a_new_bound_receipt(self):
        self.launch["helper"]["pid"] = 43
        self.save_evidence()
        with self.assertRaises(SystemExit):
            self.verify()


if __name__ == "__main__":
    unittest.main()
