package org.eu.eslzzyl.krkrgo.catalog

import android.content.Context
import android.net.Uri
import androidx.documentfile.provider.DocumentFile
import org.eu.eslzzyl.krkrgo.R

data class ImportedProject(
    val name: String,
    val uri: Uri,
)

class ProjectImporter(private val context: Context) {
    fun import(uri: Uri): Result<ImportedProject> = runCatching {
        val source = DocumentFile.fromTreeUri(context, uri)
            ?: error(context.getString(R.string.error_unable_open_selected_folder))
        require(source.isDirectory) { context.getString(R.string.error_select_project_folder) }

        val projectName = source.name?.takeIf { it.isNotBlank() }
            ?: context.getString(R.string.project_default_name)
        require(hasProjectEntryPoint(source)) {
            context.getString(R.string.error_invalid_project_folder)
        }
        ImportedProject(projectName, uri)
    }

    private fun hasProjectEntryPoint(projectRoot: DocumentFile): Boolean {
        val rootEntries = projectRoot.listFiles()
        if (rootEntries.any { it.isFile && it.name.equals("startup.tjs", ignoreCase = true) }) {
            return true
        }
        val data = rootEntries.firstOrNull { it.isDirectory && it.name.equals("data", ignoreCase = true) }
        if (data?.listFiles()?.any { it.isFile && it.name.equals("startup.tjs", ignoreCase = true) } == true) {
            return true
        }
        return containsXp3(rootEntries)
    }

    private fun containsXp3(entries: Array<DocumentFile>): Boolean {
        for (entry in entries) {
            if (entry.isFile && entry.name?.endsWith(".xp3", ignoreCase = true) == true) {
                return true
            }
            if (entry.isDirectory && containsXp3(entry.listFiles())) {
                return true
            }
        }
        return false
    }
}
