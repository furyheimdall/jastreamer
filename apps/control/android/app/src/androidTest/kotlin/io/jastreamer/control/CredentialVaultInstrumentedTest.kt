package io.jastreamer.control

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.io.File
import java.security.KeyStore
import java.util.UUID
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class CredentialVaultInstrumentedTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val vault = CredentialVault(context)

    @After
    fun cleanUp() {
        vault.delete()
        KeyStore.getInstance("AndroidKeyStore").apply {
            load(null)
            deleteEntry("${context.packageName}.control-credential.v1")
        }
    }

    @Test
    fun actualKeystorePersistsAcrossAdapterReconstructionAndDeletes() {
        val credential = "runtime-${UUID.randomUUID()}"
        vault.save(credential)

        assertEquals(credential, CredentialVault(context).load())
        CredentialVault(context).delete()
        assertNull(CredentialVault(context).load())
    }

    @Test
    fun restoredCiphertextWithoutThisAppUserKeyIsRejected() {
        vault.save("runtime-${UUID.randomUUID()}")
        KeyStore.getInstance("AndroidKeyStore").apply {
            load(null)
            deleteEntry("${context.packageName}.control-credential.v1")
        }

        assertNull(CredentialVault(context).load())
    }

    @Test
    fun copiedOrCorruptBlobIsRejectedAndRemoved() {
        val file = File(context.noBackupFilesDir, "control-credential.v1")
        file.writeBytes(byteArrayOf(0x01, 0x02, 0x03))

        assertNull(vault.load())
        assertEquals(false, file.exists())
    }
}
