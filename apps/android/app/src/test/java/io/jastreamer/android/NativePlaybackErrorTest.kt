package io.jastreamer.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class NativePlaybackErrorTest {
    @Test
    fun `native cause chain retains types but redacts messages and file names`() {
        val cause = IllegalStateException("token=inner-secret").apply {
            stackTrace = arrayOf(
                StackTraceElement(
                    "io.jastreamer.player.Decoder",
                    "openTrack",
                    "/private/user/secret-track.flac",
                    73,
                ),
            )
        }
        val failure = RuntimeException("https://user:password@example.test/media/private", cause).apply {
            stackTrace = arrayOf(
                StackTraceElement(
                    "io.jastreamer.player.Engine",
                    "prepare",
                    "credential-name.kt",
                    41,
                ),
            )
        }

        val captured = NativePlaybackError.capture(
            stage = NativePlaybackErrorStage.COMMAND,
            error = failure,
            positionMillis = 12_345L,
            occurredAtMillis = 1_700_000_000_000L,
        )

        assertEquals(0, captured.errorCode)
        assertEquals("NATIVE_ERROR", captured.errorName)
        assertEquals(listOf(RuntimeException::class.java.name, IllegalStateException::class.java.name), captured.causes.map { it.type })
        assertEquals(listOf("io.jastreamer.player.Engine#prepare:41"), captured.causes[0].stack)
        assertEquals(listOf("io.jastreamer.player.Decoder#openTrack:73"), captured.causes[1].stack)
        val exported = captured.causes.flatMap { listOf(it.type) + it.stack }.joinToString("|")
        assertFalse(exported.contains("secret", ignoreCase = true))
        assertFalse(exported.contains("password", ignoreCase = true))
        assertFalse(exported.contains("flac", ignoreCase = true))
    }

    @Test
    fun `cause stack time and position are bounded`() {
        val errors = List(6) { RuntimeException("message-$it") }
        errors.zipWithNext().forEach { (outer, inner) -> outer.initCause(inner) }
        errors.forEach { error ->
            error.stackTrace = Array(8) { index ->
                StackTraceElement(
                    "bad/path/" + "VeryLongClass".repeat(30),
                    "method name " + "VeryLongMethod".repeat(30),
                    "must-not-appear-$index.mp3",
                    index,
                )
            }
        }

        val captured = NativePlaybackError.capture(
            stage = NativePlaybackErrorStage.PREPARE,
            error = errors.first(),
            positionMillis = Long.MAX_VALUE,
            occurredAtMillis = 0L,
        )

        assertEquals(4, captured.causes.size)
        assertEquals(604_800_000L, captured.positionMillis)
        assertEquals(1L, captured.occurredAtMillis)
        captured.causes.forEach { cause ->
            assertTrue(cause.type.length <= 160)
            assertTrue(cause.type.matches(Regex("[A-Za-z0-9_.$]+")))
            assertEquals(6, cause.stack.size)
            cause.stack.forEach { frame ->
                assertTrue(frame.length <= 200)
                assertTrue(frame.matches(Regex("[A-Za-z0-9_.$]+#[A-Za-z0-9_.$<>-]+:-?[0-9]+")))
                assertFalse(frame.contains("mp3"))
            }
        }
    }

    @Test
    fun `bounded error history preserves the initial and terminal causes without duplicating a capture`() {
        val buffer = NativePlaybackErrorBuffer()
        val captures = List(5) { index ->
            NativePlaybackError.capture(
                stage = NativePlaybackErrorStage.PLAYBACK,
                error = RuntimeException("failure-$index"),
                positionMillis = index.toLong(),
                occurredAtMillis = index + 1L,
            )
        }

        buffer.add(captures[0])
        buffer.add(captures[0])
        captures.drop(1).forEach(buffer::add)

        val snapshot = buffer.snapshot()
        assertEquals(4, snapshot.size)
        assertSame(captures[0], snapshot[0])
        assertEquals(listOf(0L, 1L, 2L, 4L), snapshot.map { it.positionMillis })
        buffer.clear()
        buffer.add(captures[4])
        assertEquals(listOf(captures[4]), buffer.snapshot())
        assertEquals(listOf(0L, 1L, 2L, 4L), snapshot.map { it.positionMillis })
    }
}
