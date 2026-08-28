package io.jastreamer.control

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.system.Os
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.File
import java.io.FileOutputStream
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

internal class CredentialVault(context: Context) {
    private val applicationContext = context.applicationContext
    private val alias = "${applicationContext.packageName}.control-credential.v1"
    private val file = File(applicationContext.noBackupFilesDir, "control-credential.v1")
    private val temporaryFile = File(applicationContext.noBackupFilesDir, "control-credential.v1.tmp")

    @Synchronized
    fun load(): String? {
        if (!file.exists()) return null
        return try {
            val encoded = file.readBytes()
            require(encoded.size in 7..MAX_RECORD_BYTES)
            DataInputStream(ByteArrayInputStream(encoded)).use { input ->
                val magic = ByteArray(MAGIC.size)
                input.readFully(magic)
                require(magic.contentEquals(MAGIC))
                val ivLength = input.readUnsignedByte()
                require(ivLength in 12..32)
                val iv = ByteArray(ivLength)
                input.readFully(iv)
                val ciphertext = ByteArray(input.available())
                input.readFully(ciphertext)
                require(ciphertext.isNotEmpty())
                val cipher = Cipher.getInstance(TRANSFORMATION)
                cipher.init(Cipher.DECRYPT_MODE, getOrCreateKey(), GCMParameterSpec(128, iv))
                cipher.doFinal(ciphertext).toString(Charsets.UTF_8)
            }
        } catch (_: Exception) {
            // A restored, copied, corrupted, or invalidated blob must never be trusted.
            delete()
            null
        }
    }

    @Synchronized
    fun save(value: String) {
        val plaintext = value.toByteArray(Charsets.UTF_8)
        require(plaintext.isNotEmpty() && plaintext.size <= MAX_RECORD_BYTES)
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, getOrCreateKey())
        val ciphertext = cipher.doFinal(plaintext)
        val encoded = ByteArrayOutputStream().use { bytes ->
            DataOutputStream(bytes).use { output ->
                output.write(MAGIC)
                output.writeByte(cipher.iv.size)
                output.write(cipher.iv)
                output.write(ciphertext)
            }
            bytes.toByteArray()
        }
        applicationContext.noBackupFilesDir.mkdirs()
        FileOutputStream(temporaryFile).use { output ->
            output.write(encoded)
            output.fd.sync()
        }
        try {
            Os.rename(temporaryFile.absolutePath, file.absolutePath)
        } finally {
            temporaryFile.delete()
        }
    }

    @Synchronized
    fun delete() {
        temporaryFile.delete()
        file.delete()
    }

    private fun getOrCreateKey(): SecretKey {
        val keyStore = KeyStore.getInstance(KEYSTORE).apply { load(null) }
        (keyStore.getKey(alias, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(
                alias,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true)
                .build(),
        )
        return generator.generateKey()
    }

    private companion object {
        val MAGIC = byteArrayOf(0x4a, 0x43, 0x56, 0x01)
        const val KEYSTORE = "AndroidKeyStore"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val MAX_RECORD_BYTES = 64 * 1024
    }
}
