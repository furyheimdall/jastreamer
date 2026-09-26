// Copyright 2026 The Jastreamer Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// JNI surface for io.jastreamer.android.UsbDirectNative, the opt-in direct USB
// audio output. No Java exceptions are ever thrown from here and no media
// identifiers or payload bytes are logged.

#include <jni.h>

#include <cstdint>
#include <mutex>
#include <string>
#include <unordered_set>

#include "usb_device.h"

namespace {

using usb_direct::UsbDirectDevice;

thread_local std::string g_last_error;

std::mutex& RegistryMutex() {
  static std::mutex mutex;
  return mutex;
}

std::unordered_set<uintptr_t>& Registry() {
  static std::unordered_set<uintptr_t> registry;
  return registry;
}

void RegisterDevice(UsbDirectDevice* device) {
  std::lock_guard<std::mutex> lock(RegistryMutex());
  Registry().insert(reinterpret_cast<uintptr_t>(device));
}

// Returns the device for `handle` while the registry still knows it, or nullptr
// for a stale or forged handle.
UsbDirectDevice* LookupDevice(jlong handle) {
  if (handle == 0) {
    return nullptr;
  }
  const uintptr_t key = static_cast<uintptr_t>(handle);
  std::lock_guard<std::mutex> lock(RegistryMutex());
  if (Registry().find(key) == Registry().end()) {
    return nullptr;
  }
  return reinterpret_cast<UsbDirectDevice*>(key);
}

// Removes `handle` from the registry, returning the device exactly once so a
// double close cannot free it twice.
UsbDirectDevice* TakeDevice(jlong handle) {
  if (handle == 0) {
    return nullptr;
  }
  const uintptr_t key = static_cast<uintptr_t>(handle);
  std::lock_guard<std::mutex> lock(RegistryMutex());
  if (Registry().erase(key) == 0) {
    return nullptr;
  }
  return reinterpret_cast<UsbDirectDevice*>(key);
}

jstring ToJavaString(JNIEnv* env, const std::string& value) {
  jstring result = env->NewStringUTF(value.c_str());
  if (result == nullptr && env->ExceptionCheck()) {
    env->ExceptionClear();
  }
  return result;
}

jstring EmptyString(JNIEnv* env) { return ToJavaString(env, std::string()); }

UsbDirectDevice* RequireDevice(jlong handle) {
  UsbDirectDevice* device = LookupDevice(handle);
  if (device == nullptr) {
    g_last_error = "usb direct handle is not open";
  }
  return device;
}

}  // namespace

extern "C" JNIEXPORT jint JNICALL JNI_OnLoad(JavaVM* vm, void* /*reserved*/) {
  JNIEnv* env = nullptr;
  if (vm->GetEnv(reinterpret_cast<void**>(&env), JNI_VERSION_1_6) != JNI_OK) {
    return JNI_ERR;
  }
  return JNI_VERSION_1_6;
}

extern "C" JNIEXPORT jlong JNICALL
Java_io_jastreamer_android_UsbDirectNative_open(JNIEnv* /*env*/,
                                                jobject /*self*/, jint fd) {
  g_last_error.clear();
  std::string error;
  std::unique_ptr<UsbDirectDevice> device =
      UsbDirectDevice::OpenFromFd(static_cast<int>(fd), &error);
  if (!device) {
    g_last_error = error.empty() ? "unable to open the USB audio device" : error;
    return 0;
  }
  UsbDirectDevice* raw = device.release();
  RegisterDevice(raw);
  return static_cast<jlong>(reinterpret_cast<uintptr_t>(raw));
}

extern "C" JNIEXPORT jstring JNICALL
Java_io_jastreamer_android_UsbDirectNative_lastError(JNIEnv* env,
                                                     jobject /*self*/) {
  return ToJavaString(env, g_last_error);
}

extern "C" JNIEXPORT jstring JNICALL
Java_io_jastreamer_android_UsbDirectNative_capabilities(JNIEnv* env,
                                                        jobject /*self*/,
                                                        jlong handle) {
  UsbDirectDevice* device = RequireDevice(handle);
  if (device == nullptr) {
    return EmptyString(env);
  }
  std::string error;
  const std::string json = device->Capabilities(&error);
  if (json.empty()) {
    g_last_error =
        error.empty() ? "unable to read the device capabilities" : error;
    return EmptyString(env);
  }
  g_last_error.clear();
  return ToJavaString(env, json);
}

extern "C" JNIEXPORT jstring JNICALL
Java_io_jastreamer_android_UsbDirectNative_start(JNIEnv* env, jobject /*self*/,
                                                 jlong handle, jint sample_rate,
                                                 jint channels, jint bits) {
  UsbDirectDevice* device = RequireDevice(handle);
  if (device == nullptr) {
    return EmptyString(env);
  }
  if (sample_rate <= 0 || channels <= 0 || bits <= 0) {
    g_last_error = "sample rate, channel count and bit depth must be positive";
    return EmptyString(env);
  }
  std::string error;
  const std::string json =
      device->Start(static_cast<uint32_t>(sample_rate),
                    static_cast<uint32_t>(channels),
                    static_cast<uint32_t>(bits), &error);
  if (json.empty()) {
    g_last_error = error.empty() ? "unable to start the USB audio stream" : error;
    return EmptyString(env);
  }
  g_last_error.clear();
  return ToJavaString(env, json);
}

extern "C" JNIEXPORT jint JNICALL
Java_io_jastreamer_android_UsbDirectNative_write(JNIEnv* env, jobject /*self*/,
                                                 jlong handle, jobject buffer,
                                                 jint length,
                                                 jint source_sample_bytes,
                                                 jint source_channels) {
  UsbDirectDevice* device = RequireDevice(handle);
  if (device == nullptr) {
    return -1;
  }
  if (buffer == nullptr || length < 0) {
    g_last_error = "write needs a direct ByteBuffer and a non-negative length";
    return -1;
  }
  if (source_sample_bytes <= 0 || source_channels <= 0) {
    g_last_error = "write needs the decoded sample size and channel count";
    return -1;
  }
  void* address = env->GetDirectBufferAddress(buffer);
  const jlong capacity = env->GetDirectBufferCapacity(buffer);
  if (address == nullptr || capacity < 0) {
    g_last_error = "write requires a direct ByteBuffer";
    return -1;
  }
  if (static_cast<jlong>(length) > capacity) {
    g_last_error = "write length exceeds the buffer capacity";
    return -1;
  }
  std::string error;
  const int written = device->Write(
      static_cast<const uint8_t*>(address), static_cast<size_t>(length),
      static_cast<uint32_t>(source_sample_bytes),
      static_cast<uint32_t>(source_channels), &error);
  if (written < 0) {
    g_last_error = error.empty() ? "unable to queue audio" : error;
    return -1;
  }
  g_last_error.clear();
  return static_cast<jint>(written);
}

extern "C" JNIEXPORT void JNICALL
Java_io_jastreamer_android_UsbDirectNative_setPaused(JNIEnv* /*env*/,
                                                     jobject /*self*/,
                                                     jlong handle,
                                                     jboolean paused) {
  UsbDirectDevice* device = RequireDevice(handle);
  if (device == nullptr) {
    return;
  }
  device->SetPaused(paused == JNI_TRUE);
}

extern "C" JNIEXPORT void JNICALL
Java_io_jastreamer_android_UsbDirectNative_flush(JNIEnv* /*env*/,
                                                 jobject /*self*/,
                                                 jlong handle) {
  UsbDirectDevice* device = RequireDevice(handle);
  if (device == nullptr) {
    return;
  }
  device->Flush();
}

extern "C" JNIEXPORT jlong JNICALL
Java_io_jastreamer_android_UsbDirectNative_playedFrames(JNIEnv* /*env*/,
                                                        jobject /*self*/,
                                                        jlong handle) {
  UsbDirectDevice* device = RequireDevice(handle);
  if (device == nullptr) {
    return -1;
  }
  return static_cast<jlong>(device->PlayedFrames());
}

extern "C" JNIEXPORT jstring JNICALL
Java_io_jastreamer_android_UsbDirectNative_status(JNIEnv* env, jobject /*self*/,
                                                  jlong handle) {
  UsbDirectDevice* device = RequireDevice(handle);
  if (device == nullptr) {
    return EmptyString(env);
  }
  return ToJavaString(env, device->Status());
}

extern "C" JNIEXPORT void JNICALL
Java_io_jastreamer_android_UsbDirectNative_stop(JNIEnv* /*env*/,
                                                jobject /*self*/,
                                                jlong handle) {
  UsbDirectDevice* device = RequireDevice(handle);
  if (device == nullptr) {
    return;
  }
  device->Stop();
}

extern "C" JNIEXPORT void JNICALL
Java_io_jastreamer_android_UsbDirectNative_close(JNIEnv* /*env*/,
                                                 jobject /*self*/,
                                                 jlong handle) {
  UsbDirectDevice* device = TakeDevice(handle);
  if (device == nullptr) {
    g_last_error = "usb direct handle is not open";
    return;
  }
  device->Close();
  delete device;
}
