#!/bin/sh

create_control_android_qa_signing() {
  qa_signing_dir=$(mktemp -d "${TMPDIR:-/tmp}/jastreamer-control-android-qa-signing.XXXXXX")
  qa_keystore="$qa_signing_dir/control-qa.jks"
  qa_store_password='jastreamer-qa-only-store-password'
  qa_key_alias='control-qa'
  qa_key_password='jastreamer-qa-only-key-password'
  keytool -genkeypair -noprompt \
    -keystore "$qa_keystore" \
    -storepass "$qa_store_password" \
    -keypass "$qa_key_password" \
    -alias "$qa_key_alias" \
    -keyalg RSA -keysize 2048 -validity 1 \
    -dname 'CN=jastreamer QA only,OU=Ephemeral QA,O=jastreamer,L=Local,ST=Local,C=XX' \
    >/dev/null 2>&1
  chmod 0400 "$qa_keystore"
}

cleanup_control_android_qa_signing() {
  if [ -n "${qa_signing_dir:-}" ]; then
    rm -rf -- "$qa_signing_dir"
    qa_signing_dir=
  fi
  qa_keystore=
  qa_store_password=
  qa_key_alias=
  qa_key_password=
}
