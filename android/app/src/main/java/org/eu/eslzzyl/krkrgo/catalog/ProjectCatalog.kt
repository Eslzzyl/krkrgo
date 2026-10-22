package org.eu.eslzzyl.krkrgo.catalog

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import androidx.compose.runtime.mutableStateListOf
import java.util.UUID

data class ProjectEntry(
    val id: String,
    val name: String,
    val uri: String,
)

interface ProjectCatalog {
    val projects: List<ProjectEntry>

    fun add(name: String, uri: String): ProjectEntry
}

class PersistentProjectCatalog(context: Context) : ProjectCatalog {
    private val entries = mutableStateListOf<ProjectEntry>()
    private val preferences = context.getSharedPreferences("project_catalog", Context.MODE_PRIVATE)

    init {
        val stored = preferences.getString("entries", null)
        if (!stored.isNullOrBlank()) {
            runCatching {
                val array = JSONArray(stored)
                for (index in 0 until array.length()) {
                    val item = array.getJSONObject(index)
                    entries += ProjectEntry(
                        id = item.getString("id"),
                        name = item.getString("name"),
                        uri = item.getString("uri"),
                    )
                }
            }
        }
    }

    override val projects: List<ProjectEntry>
        get() = entries

    override fun add(name: String, uri: String): ProjectEntry {
        entries.removeAll { it.uri == uri }
        val entry = ProjectEntry(UUID.randomUUID().toString(), name, uri)
        entries += entry
        persist()
        return entry
    }

    private fun persist() {
        val array = JSONArray()
        entries.forEach { entry ->
            array.put(
                JSONObject()
                    .put("id", entry.id)
                    .put("name", entry.name)
                    .put("uri", entry.uri),
            )
        }
        preferences.edit().putString("entries", array.toString()).apply()
    }
}
