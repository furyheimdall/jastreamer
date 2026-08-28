#ifndef RUNNER_CREDENTIAL_VAULT_H_
#define RUNNER_CREDENTIAL_VAULT_H_

#include <filesystem>
#include <optional>
#include <string>

class CredentialVault {
 public:
  CredentialVault(std::filesystem::path path, std::wstring application_identity);

  std::optional<std::string> Load();
  void Save(const std::string& value);
  void Delete() noexcept;

 private:
  std::filesystem::path path_;
  std::wstring application_identity_;
};

CredentialVault CreateDefaultCredentialVault();

#endif  // RUNNER_CREDENTIAL_VAULT_H_
