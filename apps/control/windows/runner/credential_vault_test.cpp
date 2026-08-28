#include "credential_vault.h"

#include <objbase.h>
#include <windows.h>

#include <filesystem>
#include <iostream>
#include <iterator>
#include <string>

int main() {
  wchar_t temporary_root[MAX_PATH];
  if (GetTempPathW(MAX_PATH, temporary_root) == 0) return 1;
  GUID guid{};
  if (CoCreateGuid(&guid) != S_OK) return 1;
  wchar_t guid_text[40];
  StringFromGUID2(guid, guid_text, std::size(guid_text));
  const auto directory =
      std::filesystem::path(temporary_root) / L"jastreamer-vault-test" /
      guid_text;
  const auto path = directory / L"credential.dpapi";
  const auto copied = directory / L"copied.dpapi";
  const std::wstring runtime_wide(guid_text);
  const std::string runtime_value(runtime_wide.begin(), runtime_wide.end());

  CredentialVault first(path, L"test-application-identity");
  first.Save(runtime_value);
  CredentialVault after_restart(path, L"test-application-identity");
  if (after_restart.Load() != runtime_value) return 2;

  std::filesystem::copy_file(path, copied);
  CredentialVault other_application(copied, L"different-application-identity");
  if (other_application.Load().has_value() || std::filesystem::exists(copied)) {
    return 3;
  }

  after_restart.Delete();
  if (after_restart.Load().has_value()) return 4;
  std::filesystem::remove_all(directory);
  std::cout << "DPAPI user-scope save/load/delete and app binding passed\n";
  return 0;
}
