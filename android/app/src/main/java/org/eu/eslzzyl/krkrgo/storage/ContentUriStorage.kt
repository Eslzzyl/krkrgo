package org.eu.eslzzyl.krkrgo.storage

import android.content.Context
import android.net.Uri
import android.os.ParcelFileDescriptor
import androidx.documentfile.provider.DocumentFile
import org.eu.eslzzyl.krkrgo.R
import org.eu.eslzzyl.krkrgo.mobile.krkrgo.ContentURIReader
import java.nio.ByteBuffer
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong

/** Reads a persisted Storage Access Framework tree without staging its files. */
class ContentUriStorage(
    private val context: Context,
    treeUri: Uri,
) : ContentURIReader {
    private val resolver = context.contentResolver
    private val root = DocumentFile.fromTreeUri(context, treeUri)
        ?: error(context.getString(R.string.error_unable_open_selected_content_uri))
    private val nextHandle = AtomicLong(1)
    private val files = ConcurrentHashMap<Long, OpenFile>()
    private val documents = ConcurrentHashMap<String, DocumentFile>()
    private val children = ConcurrentHashMap<String, List<DocumentFile>>()

    init {
        documents[""] = root
    }

    private class OpenFile(
        val stream: ParcelFileDescriptor.AutoCloseInputStream,
        val size: Long,
    )

    override fun open(path: String): Long {
        val document = resolve(path)
        require(document?.isFile == true) {
            context.getString(R.string.error_unable_open_project_file, path)
        }
        val descriptor = resolver.openFileDescriptor(document.uri, "r")
            ?: error(context.getString(R.string.error_unable_open_project_file, path))
        val handle = nextHandle.getAndIncrement()
        files[handle] = OpenFile(
            stream = ParcelFileDescriptor.AutoCloseInputStream(descriptor),
            size = document.length().coerceAtLeast(0),
        )
        return handle
    }

    override fun size(handle: Long): Long = files[handle]?.size
        ?: error(context.getString(R.string.error_unknown_content_storage_handle, handle))

    override fun read(handle: Long, offset: Long, size: Long): ByteArray {
        require(offset >= 0 && size >= 0 && size <= Int.MAX_VALUE) {
            context.getString(R.string.error_invalid_content_read_range)
        }
        val file = files[handle]
            ?: error(context.getString(R.string.error_unknown_content_storage_handle, handle))
        if (offset >= file.size || size == 0L) {
            return ByteArray(0)
        }
        val length = minOf(size, file.size - offset).toInt()
        val bytes = ByteArray(length)
        val buffer = ByteBuffer.wrap(bytes)
        synchronized(file) {
            var position = 0
            while (position < length) {
                val count = file.stream.channel.read(buffer, offset + position)
                if (count <= 0) {
                    break
                }
                position += count
            }
            return if (position == length) bytes else bytes.copyOf(position)
        }
    }

    override fun close(handle: Long) {
        files.remove(handle)?.stream?.close()
    }

    override fun mkdir(path: String) {
        ensureDirectory(path)
    }

    override fun write(path: String, offset: Long, data: ByteArray) {
        require(offset >= 0) { context.getString(R.string.error_invalid_content_write_offset) }
        val name = path.substringAfterLast('/')
        require(name.isNotEmpty()) {
            context.getString(R.string.error_unable_write_unnamed_project_file)
        }
        val parentPath = path.substringBeforeLast('/', "")
        val parent = ensureDirectory(parentPath)
        val document = resolve(path) ?: parent.createFile("application/octet-stream", name)
            ?: error(context.getString(R.string.error_unable_create_project_save_file, path))
        val descriptor = resolver.openFileDescriptor(document.uri, "rwt")
            ?: error(context.getString(R.string.error_unable_open_project_save_file, path))
        ParcelFileDescriptor.AutoCloseOutputStream(descriptor).use { stream ->
            stream.channel.position(offset)
            stream.write(data)
            stream.flush()
        }
        documents[path] = document
    }

    override fun delete(path: String) {
        val document = resolve(path) ?: return
        require(document.delete()) {
            context.getString(R.string.error_unable_delete_project_save_entry, path)
        }
        documents.remove(path)
        children.remove(path.substringBeforeLast('/', ""))
    }

    override fun list(path: String): String {
        val directory = resolve(path)
        require(directory?.isDirectory == true) {
            context.getString(R.string.error_unable_list_project_directory, path)
        }
        return directoryChildren(path, directory).map { child ->
            val name = child.name
                ?: error(context.getString(R.string.error_project_entry_no_display_name))
            if (child.isDirectory) "D$name" else "F$name"
        }.joinToString("\u0000")
    }

    override fun stat(path: String): Long {
        val document = resolve(path)
            ?: error(context.getString(R.string.error_project_entry_not_found, path))
        return if (document.isDirectory) -1L else document.length().coerceAtLeast(0)
    }

    fun close() {
        files.keys.toList().forEach(::close)
    }

    private fun resolve(path: String): DocumentFile? {
        documents[path]?.let { return it }
        var current = root
        if (path.isBlank()) {
            return current
        }
        var currentPath = ""
        for (part in path.split('/')) {
            if (part.isBlank() || part == "." || part == "..") {
                return null
            }
            val child = directoryChildren(currentPath, current)
                .firstOrNull { it.name == part }
                ?: return null
            current = child
            currentPath = if (currentPath.isEmpty()) part else "$currentPath/$part"
            documents[currentPath] = current
        }
        return current
    }

    private fun directoryChildren(path: String, directory: DocumentFile): List<DocumentFile> {
        children[path]?.let { return it }
        val listed = directory.listFiles().toList()
        val existing = children.putIfAbsent(path, listed)
        return existing ?: listed
    }

    private fun ensureDirectory(path: String): DocumentFile {
        if (path.isBlank()) {
            return root
        }
        documents[path]?.let { document ->
            require(document.isDirectory) {
                context.getString(R.string.error_project_save_path_is_file, path)
            }
            return document
        }
        var current = root
        var currentPath = ""
        for (part in path.split('/')) {
            if (part.isBlank() || part == "." || part == "..") {
                error(context.getString(R.string.error_invalid_project_save_path, path))
            }
            val nextPath = if (currentPath.isEmpty()) part else "$currentPath/$part"
            val existing = directoryChildren(currentPath, current).firstOrNull { it.name == part }
            val child = existing ?: current.createDirectory(part)?.also {
                children.remove(currentPath)
            } ?: error(
                context.getString(R.string.error_unable_create_project_save_directory, nextPath),
            )
            require(child.isDirectory) {
                context.getString(R.string.error_project_save_path_is_file, nextPath)
            }
            current = child
            currentPath = nextPath
            documents[currentPath] = current
        }
        return current
    }
}
