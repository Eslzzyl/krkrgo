import Foundation
import SwiftUI
import UIKit
import MobileCoreServices
import Krkrgo

struct RootView: View {
    @EnvironmentObject private var appState: AppState

    var body: some View {
        Group {
            if appState.gameVisible {
                GameScreen(host: appState.gameHost)
            } else {
                LauncherScreen(
                    catalog: appState.catalog,
                    isImporting: appState.isImporting || appState.isPickingFolder,
                    isLaunching: appState.isLaunching,
                    onAddProject: { appState.beginFolderSelection() },
                    onLaunch: { appState.launch($0) },
                )
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: UIApplication.didBecomeActiveNotification)) { _ in
            appState.applicationDidBecomeActive()
        }
        .onReceive(NotificationCenter.default.publisher(for: UIApplication.willResignActiveNotification)) { _ in
            appState.applicationWillResignActive()
        }
        .onReceive(Timer.publish(every: 0.1, on: .main, in: .common).autoconnect()) { _ in
            appState.pollGameExit()
        }
        .sheet(isPresented: $appState.isPickingFolder) {
            FolderPicker(
                onPick: { url in
                    appState.isPickingFolder = false
                    appState.importFolder(url)
                },
                onCancel: { appState.isPickingFolder = false },
            )
        }
        .alert(isPresented: Binding(
            get: { appState.errorMessage != nil },
            set: { if !$0 { appState.errorMessage = nil } },
        )) {
            Alert(
                title: Text("Operation failed"),
                message: Text(appState.errorMessage ?? "Unknown error"),
                dismissButton: .default(Text("OK")),
            )
        }
    }
}

struct LauncherScreen: View {
    @ObservedObject var catalog: ProjectCatalog
    let isImporting: Bool
    let isLaunching: Bool
    let onAddProject: () -> Void
    let onLaunch: (ProjectEntry) -> Void

    var body: some View {
        NavigationView {
            Group {
                if catalog.projects.isEmpty {
                    VStack(spacing: 18) {
                        Image(systemName: "gamecontroller")
                            .font(.system(size: 52))
                            .foregroundColor(.accentColor)
                        Text("Start your library")
                            .font(.title2)
                        Text("Choose a project folder to launch your games from krkrgo.")
                            .multilineTextAlignment(.center)
                            .foregroundColor(.secondary)
                        Button(action: onAddProject) {
                            Label(isImporting ? "Opening…" : "Choose project folder", systemImage: "plus")
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(isImporting)
                    }
                    .padding(28)
                } else {
                    List {
                        ForEach(catalog.projects) { project in
                            ProjectRow(
                                project: project,
                                isLaunching: isLaunching,
                                onLaunch: { onLaunch(project) },
                            )
                        }
                    }
                    .listStyle(InsetGroupedListStyle())
                }
            }
            .navigationBarTitle("Library", displayMode: .large)
            .navigationBarItems(trailing: Button(action: onAddProject) {
                Image(systemName: "plus")
            }.disabled(isImporting))
        }
    }
}

private struct ProjectRow: View {
    let project: ProjectEntry
    let isLaunching: Bool
    let onLaunch: () -> Void

    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: "folder")
                .font(.title2)
                .foregroundColor(.accentColor)
            VStack(alignment: .leading, spacing: 4) {
                Text(project.name)
                    .font(.headline)
                    .lineLimit(1)
                Text("Project folder")
                    .font(.subheadline)
                    .foregroundColor(.secondary)
            }
            Spacer()
            Button(action: onLaunch) {
                Text(isLaunching ? "Opening…" : "Launch")
            }
            .buttonStyle(.bordered)
            .disabled(isLaunching)
        }
        .padding(.vertical, 6)
    }
}

struct GameScreen: View {
    @ObservedObject var host: GameHost

    var body: some View {
        GameView(host: host)
            .edgesIgnoringSafeArea(.all)
    }
}

struct GameView: UIViewControllerRepresentable {
    @ObservedObject var host: GameHost

    func makeUIViewController(context: Context) -> KrkrgoEbitenViewController {
        let controller = host.makeViewController()
        host.attach(controller)
        return controller
    }

    func updateUIViewController(_ uiViewController: KrkrgoEbitenViewController, context: Context) {
        host.attach(uiViewController)
    }

}

struct FolderPicker: UIViewControllerRepresentable {
    let onPick: (URL) -> Void
    let onCancel: () -> Void

    func makeCoordinator() -> Coordinator {
        Coordinator(onPick: onPick, onCancel: onCancel)
    }

    func makeUIViewController(context: Context) -> UIDocumentPickerViewController {
        let picker = UIDocumentPickerViewController(documentTypes: [kUTTypeFolder as String], in: .open)
        picker.delegate = context.coordinator
        picker.allowsMultipleSelection = false
        return picker
    }

    func updateUIViewController(_ uiViewController: UIDocumentPickerViewController, context: Context) {}

    final class Coordinator: NSObject, UIDocumentPickerDelegate {
        let onPick: (URL) -> Void
        let onCancel: () -> Void

        init(onPick: @escaping (URL) -> Void, onCancel: @escaping () -> Void) {
            self.onPick = onPick
            self.onCancel = onCancel
        }

        func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
            if let url = urls.first {
                onPick(url)
            } else {
                onCancel()
            }
        }

        func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
            onCancel()
        }
    }
}
