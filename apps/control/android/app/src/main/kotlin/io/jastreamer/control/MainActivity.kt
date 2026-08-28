package io.jastreamer.control

import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        val vault = CredentialVault(applicationContext)
        MethodChannel(
            flutterEngine.dartExecutor.binaryMessenger,
            "io.jastreamer.control/credential-vault",
        ).setMethodCallHandler { call, result ->
            try {
                when (call.method) {
                    "load" -> result.success(vault.load())
                    "save" -> {
                        val value = call.arguments as? String
                            ?: throw IllegalArgumentException("credential record required")
                        vault.save(value)
                        result.success(null)
                    }
                    "delete" -> {
                        vault.delete()
                        result.success(null)
                    }
                    else -> result.notImplemented()
                }
            } catch (_: Exception) {
                // Never attach arguments or crypto exception text to diagnostics.
                result.error("credential_vault_unavailable", "Secure credential storage is unavailable.", null)
            }
        }
    }
}
