@file:OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class)

package org.eu.eslzzyl.krkrgo.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.SportsEsports
import androidx.compose.material3.Button
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import org.eu.eslzzyl.krkrgo.catalog.ProjectCatalog
import org.eu.eslzzyl.krkrgo.catalog.ProjectEntry

@Composable
fun LauncherScreen(
    catalog: ProjectCatalog,
    selectedProjectId: String?,
    operationError: String?,
    onDismissOperationError: () -> Unit,
    isImporting: Boolean,
    isLaunching: Boolean,
    onAddProject: () -> Unit,
    onLaunch: (ProjectEntry) -> Unit,
) {
    val snackbarHostState = remember { SnackbarHostState() }

    LaunchedEffect(operationError) {
        if (!operationError.isNullOrBlank()) {
            snackbarHostState.showSnackbar(operationError)
            onDismissOperationError()
        }
    }

    Scaffold(
        modifier = Modifier.fillMaxSize(),
        contentWindowInsets = WindowInsets.safeDrawing,
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text("Library", style = MaterialTheme.typography.headlineSmall)
                        Text(
                            "Your game projects",
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.surface,
                ),
            )
        },
        floatingActionButton = {
            if (catalog.projects.isNotEmpty()) {
                ExtendedFloatingActionButton(
                    onClick = { if (!isImporting) onAddProject() },
                    icon = { Icon(Icons.Outlined.Add, contentDescription = null) },
                    text = { Text(if (isImporting) "Importing..." else "Add project") },
                )
            }
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
    ) { innerPadding ->
        BoxWithConstraints(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPadding),
        ) {
            if (catalog.projects.isEmpty()) {
                EmptyLibrary(isImporting = isImporting, onAddProject = onAddProject)
            } else if (maxWidth >= 840.dp) {
                ProjectGrid(
                    projects = catalog.projects,
                    selectedProjectId = selectedProjectId,
                    isLaunching = isLaunching,
                    onLaunch = onLaunch,
                )
            } else {
                ProjectList(
                    projects = catalog.projects,
                    selectedProjectId = selectedProjectId,
                    isLaunching = isLaunching,
                    onLaunch = onLaunch,
                )
            }
        }
    }

}
@Composable
private fun EmptyLibrary(
    isImporting: Boolean,
    onAddProject: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = 28.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Surface(
            modifier = Modifier.size(96.dp),
            shape = RoundedCornerShape(28.dp),
            color = MaterialTheme.colorScheme.primaryContainer,
        ) {
            Box(contentAlignment = Alignment.Center) {
                Icon(
                    imageVector = Icons.Outlined.SportsEsports,
                    contentDescription = null,
                    modifier = Modifier.size(48.dp),
                    tint = MaterialTheme.colorScheme.onPrimaryContainer,
                )
            }
        }
        Spacer(Modifier.size(28.dp))
        Text(
            "Start your library",
            style = MaterialTheme.typography.headlineSmall,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.size(12.dp))
        Text(
            "Choose a project folder to launch your games from krkrgo.",
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.size(28.dp))
        Button(onClick = onAddProject, enabled = !isImporting) {
            if (isImporting) {
                CircularProgressIndicator(
                    modifier = Modifier.size(20.dp),
                    strokeWidth = 2.dp,
                    color = MaterialTheme.colorScheme.onPrimary,
                )
            } else {
                Icon(Icons.Outlined.Add, contentDescription = null)
            }
            Spacer(Modifier.width(8.dp))
            Text(if (isImporting) "Importing..." else "Choose project folder")
        }
    }
}

@Composable
private fun ProjectList(
    projects: List<ProjectEntry>,
    selectedProjectId: String?,
    isLaunching: Boolean,
    onLaunch: (ProjectEntry) -> Unit,
) {
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, top = 12.dp, end = 20.dp, bottom = 112.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        item { ProjectSectionHeader(projects.size) }
        items(projects, key = { it.id }) { project ->
            ProjectCard(
                project = project,
                selected = project.id == selectedProjectId,
                isLaunching = isLaunching,
                onLaunch = { onLaunch(project) },
            )
        }
    }
}

@Composable
private fun ProjectGrid(
    projects: List<ProjectEntry>,
    selectedProjectId: String?,
    isLaunching: Boolean,
    onLaunch: (ProjectEntry) -> Unit,
) {
    LazyVerticalGrid(
        columns = GridCells.Adaptive(minSize = 300.dp),
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 28.dp, top = 16.dp, end = 28.dp, bottom = 112.dp),
        horizontalArrangement = Arrangement.spacedBy(20.dp),
        verticalArrangement = Arrangement.spacedBy(20.dp),
    ) {
        item { ProjectSectionHeader(projects.size) }
        items(projects, key = { it.id }) { project ->
            ProjectCard(
                project = project,
                selected = project.id == selectedProjectId,
                isLaunching = isLaunching,
                onLaunch = { onLaunch(project) },
            )
        }
    }
}

@Composable
private fun ProjectSectionHeader(projectCount: Int) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Text("Projects", style = MaterialTheme.typography.titleLarge)
        Text(
            "$projectCount saved project${if (projectCount == 1) "" else "s"}",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun ProjectCard(
    project: ProjectEntry,
    selected: Boolean,
    isLaunching: Boolean,
    onLaunch: () -> Unit,
) {
    ElevatedCard(
        modifier = Modifier.fillMaxWidth(),
        colors = CardDefaults.elevatedCardColors(
            containerColor = if (selected) {
                MaterialTheme.colorScheme.secondaryContainer
            } else {
                MaterialTheme.colorScheme.surfaceContainerLow
            },
        ),
    ) {
        Column(modifier = Modifier.padding(20.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Surface(
                    modifier = Modifier.size(52.dp),
                    shape = RoundedCornerShape(16.dp),
                    color = MaterialTheme.colorScheme.primaryContainer,
                ) {
                    Box(contentAlignment = Alignment.Center) {
                        Icon(
                            Icons.Outlined.FolderOpen,
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.onPrimaryContainer,
                        )
                    }
                }
                Spacer(Modifier.width(14.dp))
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        project.name,
                        style = MaterialTheme.typography.titleLarge,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        if (selected) "Ready to resume" else "Project folder",
                        style = MaterialTheme.typography.labelLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Spacer(Modifier.size(18.dp))
            Text(
                "Reads directly from the selected folder",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Spacer(Modifier.size(18.dp))
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                FilledTonalButton(onClick = onLaunch, enabled = !isLaunching) {
                    Icon(Icons.Outlined.SportsEsports, contentDescription = null)
                    Spacer(Modifier.width(8.dp))
                    Text(if (isLaunching) "Opening..." else if (selected) "Resume" else "Launch")
                }
            }
        }
    }
}
