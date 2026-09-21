package io.jastreamer.android

import android.media.MediaCodec
import androidx.annotation.OptIn
import androidx.media3.common.PlaybackException
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.audio.AudioSink
import androidx.media3.exoplayer.mediacodec.MediaCodecDecoderException
import org.json.JSONArray
import org.json.JSONObject

internal enum class NativePlaybackErrorStage(val wireValue: String) {
    PREPARE("prepare"),
    COMMAND("command"),
    PLAYBACK("playback"),
}

internal data class NativePlaybackErrorCause(
    val type: String,
    val stack: List<String>,
    val httpStatus: Int? = null,
    val platformCode: Int? = null,
) {
    fun toJson(): JSONObject = JSONObject()
        .put("type", type)
        .put("stack", JSONArray(stack))
        .also { value ->
            httpStatus?.let { value.put("http_status", it) }
            platformCode?.let { value.put("platform_code", it) }
        }
}

internal data class NativePlaybackError(
    val stage: NativePlaybackErrorStage,
    val errorCode: Int,
    val errorName: String,
    val occurredAtMillis: Long,
    val positionMillis: Long,
    val causes: List<NativePlaybackErrorCause>,
) {
    fun toJson(): JSONObject = JSONObject()
        .put("stage", stage.wireValue)
        .put("error_code", errorCode)
        .put("error_name", errorName)
        .put("occurred_at_ms", occurredAtMillis)
        .put("position_ms", positionMillis)
        .put("causes", JSONArray(causes.map(NativePlaybackErrorCause::toJson)))

    companion object {
        private val ERROR_NAME = Regex("[A-Z0-9_]{1,96}")

        @OptIn(UnstableApi::class)
        fun capture(
            stage: NativePlaybackErrorStage,
            error: Throwable,
            positionMillis: Long,
            occurredAtMillis: Long = System.currentTimeMillis(),
        ): NativePlaybackError {
            val playbackError = error as? PlaybackException
            val code = playbackError?.errorCode?.takeIf { it in 1..MAX_ERROR_CODE } ?: 0
            val name = if (code == 0) {
                NATIVE_ERROR_NAME
            } else {
                playbackError?.getErrorCodeName()?.takeIf(ERROR_NAME::matches) ?: UNKNOWN_MEDIA3_ERROR_NAME
            }
            return NativePlaybackError(
                stage = stage,
                errorCode = code,
                errorName = name,
                occurredAtMillis = occurredAtMillis.coerceAtLeast(1L),
                positionMillis = positionMillis.coerceIn(0L, MAX_POSITION_MILLIS),
                causes = boundedCauses(error),
            )
        }

        @OptIn(UnstableApi::class)
        private fun boundedCauses(error: Throwable): List<NativePlaybackErrorCause> {
            val result = ArrayList<NativePlaybackErrorCause>(MAX_CAUSES)
            val seen = java.util.Collections.newSetFromMap(java.util.IdentityHashMap<Throwable, Boolean>())
            var current: Throwable? = error
            while (current != null && result.size < MAX_CAUSES && seen.add(current)) {
                result += NativePlaybackErrorCause(
                    type = sanitizeType(current.javaClass.name),
                    stack = current.stackTrace.asSequence()
                        .take(MAX_STACK_FRAMES)
                        .map(::sanitizeFrame)
                        .toList(),
                    httpStatus = (current as? HttpDataSource.InvalidResponseCodeException)
                        ?.responseCode
                        ?.takeIf { it in 100..599 },
                    platformCode = platformCode(current),
                )
                current = current.cause
            }
            return result
        }

        private fun sanitizeType(value: String): String {
            val result = StringBuilder(minOf(value.length, MAX_CAUSE_TYPE_LENGTH))
            for (character in value) {
                if (result.length >= MAX_CAUSE_TYPE_LENGTH) break
                result.append(
                    if (
                        character in 'A'..'Z' || character in 'a'..'z' ||
                        character in '0'..'9' || character == '_' ||
                        character == '.' || character == '$'
                    ) {
                        character
                    } else {
                        '_'
                    },
                )
            }
            return result.toString().ifEmpty { Throwable::class.java.name }
        }

        private fun sanitizeFrame(frame: StackTraceElement): String {
            val line = frame.lineNumber.toString()
            val available = (MAX_STACK_FRAME_LENGTH - line.length - 2).coerceAtLeast(2)
            val classBudget = available * 2 / 3
            val methodBudget = available - classBudget
            val className = sanitizeType(frame.className).take(classBudget)
            val methodName = sanitizeSymbol(frame.methodName, methodBudget)
            return "$className#$methodName:$line"
        }

        private fun sanitizeSymbol(value: String, maximum: Int): String {
            if (maximum <= 0) return "_"
            val result = StringBuilder(minOf(value.length, maximum))
            for (character in value) {
                if (result.length >= maximum) break
                result.append(if (character.isAsciiSymbol()) character else '_')
            }
            return result.toString().ifEmpty { "_" }
        }

        @OptIn(UnstableApi::class)
        private fun platformCode(error: Throwable): Int? = when (error) {
            is AudioSink.InitializationException -> error.audioTrackState
            is AudioSink.WriteException -> error.errorCode
            is MediaCodecDecoderException -> error.errorCode.takeIf { it != 0 }
            is MediaCodec.CodecException -> error.errorCode
            else -> null
        }

        private fun Char.isAsciiSymbol(): Boolean =
            this in 'A'..'Z' || this in 'a'..'z' || this in '0'..'9' ||
                this == '_' || this == '.' || this == '$' ||
                this == '<' || this == '>' || this == '-'

        private const val NATIVE_ERROR_NAME = "NATIVE_ERROR"
        private const val UNKNOWN_MEDIA3_ERROR_NAME = "ERROR_CODE_UNKNOWN"
        private const val MAX_ERROR_CODE = 999_999
        private const val MAX_POSITION_MILLIS = 604_800_000L
        private const val MAX_CAUSES = 4
        private const val MAX_CAUSE_TYPE_LENGTH = 160
        private const val MAX_STACK_FRAMES = 6
        private const val MAX_STACK_FRAME_LENGTH = 200
    }
}

internal class NativePlaybackErrorBuffer {
    private val values = ArrayList<NativePlaybackError>(4)

    fun add(value: NativePlaybackError) {
        if (values.any { it === value }) return
        if (values.size < 4) values += value else values[3] = value
    }

    fun clear() = values.clear()

    fun snapshot(): List<NativePlaybackError> = values.toList()
}

