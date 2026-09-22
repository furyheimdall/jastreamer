package io.jastreamer.android

import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.BaseDataSource
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DataSpec
import java.io.EOFException
import java.io.IOException

/** Opens app-owned audio by durable local ID, never by a stored filesystem path or URL. */
@OptIn(UnstableApi::class)
internal class OfflineAudioDataSource(
    private val library: OfflineLibrary,
) : BaseDataSource(false) {
    private var openedUri: Uri? = null
    private var handle: OfflineAudioHandle? = null
    private var remaining = 0L
    private var opened = false

    override fun open(dataSpec: DataSpec): Long {
        transferInitializing(dataSpec)
        val trackId = trackId(dataSpec.uri)
            ?: throw IOException("Invalid offline media identity")
        val audio = library.openAudio(trackId)
        try {
            if (dataSpec.position < 0L || dataSpec.position > audio.length) {
                throw EOFException("Offline media position is outside the file")
            }
            audio.input.channel.position(dataSpec.position)
            val available = audio.length - dataSpec.position
            remaining = if (dataSpec.length == C.LENGTH_UNSET.toLong()) {
                available
            } else {
                minOf(available, dataSpec.length)
            }
            handle = audio
            openedUri = dataSpec.uri
            opened = true
            transferStarted(dataSpec)
            return remaining
        } catch (error: Throwable) {
            audio.close()
            throw error
        }
    }

    override fun read(buffer: ByteArray, offset: Int, length: Int): Int {
        if (length == 0) return 0
        if (remaining == 0L) return C.RESULT_END_OF_INPUT
        val audio = handle ?: throw IOException("Offline audio is not open")
        val requested = minOf(length.toLong(), remaining).toInt()
        val read = audio.input.read(buffer, offset, requested)
        if (read < 0) {
            remaining = 0L
            return C.RESULT_END_OF_INPUT
        }
        remaining -= read
        bytesTransferred(read)
        return read
    }

    override fun getUri(): Uri? = openedUri

    override fun close() {
        openedUri = null
        remaining = 0L
        val wasOpened = opened
        opened = false
        try {
            handle?.close()
        } finally {
            handle = null
            if (wasOpened) transferEnded()
        }
    }

    class Factory(private val library: OfflineLibrary) : DataSource.Factory {
        override fun createDataSource(): DataSource = OfflineAudioDataSource(library)
    }

    companion object {
        fun uri(trackId: String): Uri {
            require(trackId.isNotBlank() && trackId.none(Char::isISOControl))
            return Uri.Builder().scheme(SCHEME).authority(trackId).build()
        }

        internal fun trackId(uri: Uri): String? {
            if (uri.scheme != SCHEME || !uri.path.isNullOrEmpty() || uri.query != null || uri.fragment != null) return null
            val id = uri.authority ?: return null
            if (id.isBlank() || id.any(Char::isISOControl) || id.contains('@') || id.contains(':')) return null
            return id
        }

        private const val SCHEME = "offline"
    }
}
