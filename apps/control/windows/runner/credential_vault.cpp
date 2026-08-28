#include "credential_vault.h"

#include <appmodel.h>
#include <dpapi.h>
#include <shlobj.h>
#include <windows.h>

#include <fstream>
#include <iterator>
#include <stdexcept>
#include <system_error>
#include <utility>
#include <vector>

namespace {
constexpr size_t kMaximumRecordBytes = 64 * 1024;

std::wstring ApplicationIdentity() {
  UINT32 length = 0;
  LONG result = GetCurrentPackageFamilyName(&length, nullptr);
  if (result == ERROR_INSUFFICIENT_BUFFER) {
    std::wstring family(length, L'\0');
    if (GetCurrentPackageFamilyName(&length, family.data()) == ERROR_SUCCESS) {
      family.resize(length > 0 ? length - 1 : 0);
      return L"package:" + family;
    }
  }

  std::vector<wchar_t> module_path(32768);
  DWORD written = GetModuleFileNameW(nullptr, module_path.data(),
                                     static_cast<DWORD>(module_path.size()));
  if (written == 0 ||
      written == static_cast<DWORD>(module_path.size())) {
    throw std::runtime_error("application identity unavailable");
  }
  return L"unpackaged:" +
         std::filesystem::weakly_canonical(
             std::wstring(module_path.data(), written)).wstring();
}

std::filesystem::path StoragePath(const std::wstring& identity) {
  PWSTR local_data = nullptr;
  if (SHGetKnownFolderPath(FOLDERID_LocalAppData, KF_FLAG_CREATE, nullptr,
                           &local_data) != S_OK) {
    throw std::runtime_error("local application data unavailable");
  }
  std::filesystem::path root(local_data);
  CoTaskMemFree(local_data);

  constexpr wchar_t kPackagePrefix[] = L"package:";
  if (identity.rfind(kPackagePrefix, 0) == 0) {
    return root / L"Packages" / identity.substr(std::size(kPackagePrefix) - 1) /
           L"LocalState" / L"control-credential.v1.dpapi";
  }
  return root / L"jastreamer-control" / L"control-credential.v1.dpapi";
}

DATA_BLOB Blob(void* data, size_t size) {
  DATA_BLOB blob{};
  blob.pbData = static_cast<BYTE*>(data);
  blob.cbData = static_cast<DWORD>(size);
  return blob;
}

DATA_BLOB Entropy(const std::wstring& identity) {
  return Blob(const_cast<wchar_t*>(identity.data()),
              identity.size() * sizeof(wchar_t));
}
}  // namespace

CredentialVault::CredentialVault(std::filesystem::path path,
                                 std::wstring application_identity)
    : path_(std::move(path)),
      application_identity_(std::move(application_identity)) {}

std::optional<std::string> CredentialVault::Load() {
  if (!std::filesystem::exists(path_)) return std::nullopt;
  try {
    const auto size = std::filesystem::file_size(path_);
    if (size == 0 || size > kMaximumRecordBytes * 2) {
      throw std::runtime_error("invalid credential record");
    }
    std::vector<BYTE> encrypted(static_cast<size_t>(size));
    std::ifstream input(path_, std::ios::binary);
    input.read(reinterpret_cast<char*>(encrypted.data()),
               static_cast<std::streamsize>(encrypted.size()));
    if (!input || static_cast<size_t>(input.gcount()) != encrypted.size()) {
      throw std::runtime_error("credential read failed");
    }

    DATA_BLOB ciphertext = Blob(encrypted.data(), encrypted.size());
    DATA_BLOB entropy = Entropy(application_identity_);
    DATA_BLOB plaintext{};
    if (!CryptUnprotectData(&ciphertext, nullptr, &entropy, nullptr, nullptr,
                            CRYPTPROTECT_UI_FORBIDDEN, &plaintext)) {
      throw std::runtime_error("credential decryption failed");
    }
    std::string value(reinterpret_cast<char*>(plaintext.pbData),
                      plaintext.cbData);
    SecureZeroMemory(plaintext.pbData, plaintext.cbData);
    LocalFree(plaintext.pbData);
    if (value.empty() || value.size() > kMaximumRecordBytes) {
      throw std::runtime_error("invalid credential payload");
    }
    return value;
  } catch (...) {
    Delete();
    return std::nullopt;
  }
}

void CredentialVault::Save(const std::string& value) {
  if (value.empty() || value.size() > kMaximumRecordBytes) {
    throw std::invalid_argument("invalid credential record");
  }
  DATA_BLOB plaintext =
      Blob(const_cast<char*>(value.data()), value.size());
  DATA_BLOB entropy = Entropy(application_identity_);
  DATA_BLOB ciphertext{};
  if (!CryptProtectData(&plaintext, nullptr, &entropy, nullptr, nullptr,
                        CRYPTPROTECT_UI_FORBIDDEN, &ciphertext)) {
    throw std::runtime_error("credential encryption failed");
  }

  std::filesystem::create_directories(path_.parent_path());
  auto temporary = path_;
  temporary += L".tmp";
  try {
    std::ofstream output(temporary, std::ios::binary | std::ios::trunc);
    output.write(reinterpret_cast<const char*>(ciphertext.pbData),
                 ciphertext.cbData);
    output.flush();
    if (!output) throw std::runtime_error("credential write failed");
    output.close();
    if (!MoveFileExW(temporary.c_str(), path_.c_str(),
                     MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH)) {
      throw std::runtime_error("credential replace failed");
    }
  } catch (...) {
    std::error_code ignored;
    std::filesystem::remove(temporary, ignored);
    SecureZeroMemory(ciphertext.pbData, ciphertext.cbData);
    LocalFree(ciphertext.pbData);
    throw;
  }
  SecureZeroMemory(ciphertext.pbData, ciphertext.cbData);
  LocalFree(ciphertext.pbData);
}

void CredentialVault::Delete() noexcept {
  std::error_code ignored;
  std::filesystem::remove(path_, ignored);
  auto temporary = path_;
  temporary += L".tmp";
  std::filesystem::remove(temporary, ignored);
}

CredentialVault CreateDefaultCredentialVault() {
  auto identity = ApplicationIdentity();
  return CredentialVault(StoragePath(identity), std::move(identity));
}
