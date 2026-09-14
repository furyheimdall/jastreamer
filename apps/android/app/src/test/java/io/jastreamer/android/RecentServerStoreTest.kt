package io.jastreamer.android

import java.nio.file.Files
import java.nio.file.attribute.PosixFilePermission
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class RecentServerStoreTest {
    @get:Rule val temporary = TemporaryFolder()

    @Test
    fun failedWritesCannotBecomeVisibleToCurrentOrReopenedStore() {
        val directory = temporary.newFolder("client")
        val store = RecentServerStore(directory)
        val server = ServerEndpoint("877f173c-d9c0-4eeb-8e49-887e2f044996", "Server", "0.2.0", "http://192.168.1.8:8080")
        store.remember(server)
        val permissions = Files.getPosixFilePermissions(directory.toPath())
        try {
            Files.setPosixFilePermissions(directory.toPath(), setOf(PosixFilePermission.OWNER_READ, PosixFilePermission.OWNER_EXECUTE))
            assertFalse("Run this filesystem regression as an unprivileged user", Files.isWritable(directory.toPath()))
            assertEquals(ClientErrorCode.STORAGE, assertThrows(ClientException::class.java) { store.setLanguage("ko") }.code)
            assertEquals(ClientErrorCode.STORAGE, assertThrows(ClientException::class.java) { store.remove(server) }.code)
            assertEquals("en", store.language())
            assertEquals(listOf(server), store.list())
            val reopened = RecentServerStore(directory)
            assertEquals("en", reopened.language())
            assertEquals(listOf(server), reopened.list())
        } finally {
            Files.setPosixFilePermissions(directory.toPath(), permissions)
        }
    }

    @Test
    fun removingOnePortDoesNotForgetTheOtherOrigin() {
        val store = RecentServerStore(temporary.newFolder("origins"))
        val first = ServerEndpoint("877f173c-d9c0-4eeb-8e49-887e2f044996", "Server", "0.2.0", "http://192.168.1.8:8080")
        val second = first.copy(origin = "http://192.168.1.8:8081")
        store.remember(first)
        store.remember(second)
        store.remove(first)
        assertEquals(listOf(second), store.list())
    }
}
