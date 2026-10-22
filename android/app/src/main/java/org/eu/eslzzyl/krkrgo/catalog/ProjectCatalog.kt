package org.eu.eslzzyl.krkrgo.catalog

import androidx.compose.runtime.mutableStateListOf
import java.util.UUID

data class ProjectEntry(
    val id: String,
    val name: String,
    val path: String,
)

interface ProjectCatalog {
    val projects: List<ProjectEntry>

    fun add(name: String, path: String): ProjectEntry
}

class InMemoryProjectCatalog : ProjectCatalog {
    private val entries = mutableStateListOf<ProjectEntry>()

    override val projects: List<ProjectEntry>
        get() = entries

    override fun add(name: String, path: String): ProjectEntry {
        val entry = ProjectEntry(UUID.randomUUID().toString(), name, path)
        entries += entry
        return entry
    }
}
