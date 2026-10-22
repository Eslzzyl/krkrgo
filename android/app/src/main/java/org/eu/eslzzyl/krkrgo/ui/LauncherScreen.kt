package org.eu.eslzzyl.krkrgo.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import org.eu.eslzzyl.krkrgo.catalog.ProjectCatalog
import org.eu.eslzzyl.krkrgo.catalog.ProjectEntry

@Composable
fun LauncherScreen(
    catalog: ProjectCatalog,
    selectedProjectId: String?,
    onLaunch: (ProjectEntry) -> Unit,
) {
    var projectName by remember { mutableStateOf("") }
    var projectPath by remember { mutableStateOf("") }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text("krkrgo")
        Text("Projects")
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedTextField(
                modifier = Modifier.weight(1f),
                value = projectName,
                onValueChange = { projectName = it },
                label = { Text("Name") },
                singleLine = true,
            )
            OutlinedTextField(
                modifier = Modifier.weight(2f),
                value = projectPath,
                onValueChange = { projectPath = it },
                label = { Text("Project path") },
                singleLine = true,
            )
        }
        Button(
            onClick = {
                catalog.add(
                    name = projectName.ifBlank { "Project" },
                    path = projectPath,
                )
                projectName = ""
                projectPath = ""
            },
            enabled = projectPath.isNotBlank(),
        ) {
            Text("Add project")
        }
        LazyColumn(
            modifier = Modifier.fillMaxWidth(),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            items(catalog.projects, key = { it.id }) { project ->
                Card(modifier = Modifier.fillMaxWidth()) {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(16.dp),
                        horizontalArrangement = Arrangement.SpaceBetween,
                    ) {
                        Column(modifier = Modifier.weight(1f)) {
                            Text(project.name)
                            Text(project.path)
                        }
                        Button(onClick = { onLaunch(project) }) {
                            Text(if (project.id == selectedProjectId) "Resume" else "Launch")
                        }
                    }
                }
            }
        }
    }
}
