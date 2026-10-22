import Foundation
import SwiftUI
import UIKit
import Krkrgo

struct ProjectEntry: Identifiable, Codable, Equatable {
    let id: UUID
    var name: String
    var bookmark: Data
}

final class ProjectCatalog: ObservableObject {
    @Published private(set) var projects: [ProjectEntry] = []

    private let defaults: UserDefaults
    private let storageKey = "project_catalog"

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        load()
    }

    @discardableResult
    func add(name: String, bookmark: Data) -> ProjectEntry {
        projects.removeAll { $0.bookmark == bookmark }
        let entry = ProjectEntry(id: UUID(), name: name, bookmark: bookmark)
        projects.append(entry)
        persist()
        return entry
    }

    private func load() {
        guard let data = defaults.data(forKey: storageKey) else {
            return
        }
        projects = (try? JSONDecoder().decode([ProjectEntry].self, from: data)) ?? []
    }

    private func persist() {
        guard let data = try? JSONEncoder().encode(projects) else {
            return
        }
        defaults.set(data, forKey: storageKey)
    }
}

struct ImportedProject {
    let name: String
    let bookmark: Data
}

enum ProjectImportError: LocalizedError {
    case invalidFolder
    case missingEntryPoint
    case staleBookmark

    var errorDescription: String? {
        switch self {
        case .invalidFolder:
            return "The selected item is not a project folder."
        case .missingEntryPoint:
            return "The selected folder does not contain startup.tjs or an XP3 archive."
        case .staleBookmark:
            return "The saved project folder needs to be selected again."
        }
    }
}

enum ProjectImporter {
    static func importProject(from url: URL) throws -> ImportedProject {
        let scoped = url.startAccessingSecurityScopedResource()
        defer {
            if scoped {
                url.stopAccessingSecurityScopedResource()
            }
        }

        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: url.path, isDirectory: &isDirectory), isDirectory.boolValue else {
            throw ProjectImportError.invalidFolder
        }
        guard hasEntryPoint(url) else {
            throw ProjectImportError.missingEntryPoint
        }
        let bookmark = try url.bookmarkData(
            options: [],
            includingResourceValuesForKeys: nil,
            relativeTo: nil,
        )
        let name = url.lastPathComponent.isEmpty ? "Project" : url.lastPathComponent
        return ImportedProject(name: name, bookmark: bookmark)
    }

    private static func hasEntryPoint(_ root: URL) -> Bool {
        let fileManager = FileManager.default
        let startup = root.appendingPathComponent("startup.tjs")
        if fileManager.fileExists(atPath: startup.path) {
            return true
        }

        let dataStartup = root.appendingPathComponent("data/startup.tjs")
        if fileManager.fileExists(atPath: dataStartup.path) {
            return true
        }

        guard let enumerator = fileManager.enumerator(
            at: root,
            includingPropertiesForKeys: [.isDirectoryKey, .isRegularFileKey],
            options: [.skipsHiddenFiles],
        ) else {
            return false
        }
        for case let candidate as URL in enumerator {
            if candidate.pathExtension.caseInsensitiveCompare("xp3") == .orderedSame {
                return true
            }
        }
        return false
    }
}

final class GameHost: ObservableObject {
    @Published private(set) var isActive = true

    private var storage: SecurityScopedStorage?
    private var viewController: KrkrgoEbitenViewController?

    func open(_ project: ProjectEntry) throws {
        let nextStorage = try SecurityScopedStorage(bookmark: project.bookmark)
        try nextStorage.start()
        do {
            var openError: NSError?
            guard KrkrgoOpenGameWithContentURI(
                nextStorage.root.absoluteString,
                nextStorage,
                &openError,
            ) else {
                throw openError ?? CocoaError(.fileReadUnknown)
            }
        } catch {
            nextStorage.close()
            throw error
        }
        closeStorage()
        storage = nextStorage
        isActive = true
        KrkrgoResumeGame()
    }

    func attach(_ viewController: KrkrgoEbitenViewController) {
        self.viewController = viewController
        if isActive {
            viewController.resumeGame()
        } else {
            viewController.suspendGame()
        }
    }

    func makeViewController() -> KrkrgoEbitenViewController {
        if let viewController {
            return viewController
        }
        let viewController = KrkrgoEbitenViewController(nibName: nil, bundle: nil)
        self.viewController = viewController
        return viewController
    }

    func detach(_ viewController: KrkrgoEbitenViewController) {
        if self.viewController === viewController {
            self.viewController = nil
        }
    }

    func pause() {
        guard storage != nil else {
            return
        }
        isActive = false
        viewController?.suspendGame()
        KrkrgoPauseGame()
    }

    func resume() {
        guard storage != nil else {
            return
        }
        isActive = true
        KrkrgoResumeGame()
        viewController?.resumeGame()
    }

    func close() {
        isActive = false
        viewController?.suspendGame()
        KrkrgoCloseGame()
        closeStorage()
    }

    private func closeStorage() {
        storage?.close()
        storage = nil
    }

    deinit {
        viewController?.suspendGame()
        KrkrgoCloseGame()
        closeStorage()
    }
}

final class AppState: ObservableObject {
    @Published var gameVisible = false
    @Published var isPickingFolder = false
    @Published var isImporting = false
    @Published var isLaunching = false
    @Published var errorMessage: String?

    let catalog = ProjectCatalog()
    let gameHost = GameHost()

    func beginFolderSelection() {
        isPickingFolder = true
    }

    func importFolder(_ url: URL) {
        isImporting = true
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let result = Result { try ProjectImporter.importProject(from: url) }
            DispatchQueue.main.async {
                guard let self else { return }
                self.isImporting = false
                switch result {
                case .success(let imported):
                    self.catalog.add(name: imported.name, bookmark: imported.bookmark)
                case .failure(let error):
                    self.errorMessage = error.localizedDescription
                }
            }
        }
    }

    func launch(_ project: ProjectEntry) {
        guard !isLaunching else { return }
        isLaunching = true
        do {
            try gameHost.open(project)
            gameVisible = true
            OrientationController.setLandscape()
        } catch {
            errorMessage = error.localizedDescription
        }
        isLaunching = false
    }

    func closeGame() {
        gameHost.close()
        gameVisible = false
        OrientationController.setPortrait()
    }

    func pollGameExit() {
        guard gameVisible, KrkrgoGameExitRequested() else {
            return
        }
        closeGame()
    }

    func applicationDidBecomeActive() {
        if gameVisible {
            gameHost.resume()
        }
    }

    func applicationWillResignActive() {
        if gameVisible {
            gameHost.pause()
        }
    }
}

enum OrientationController {
    static func setLandscape() {
        request(.landscape)
    }

    static func setPortrait() {
        request(.portrait)
    }

    private static func request(_ mask: UIInterfaceOrientationMask) {
        if #available(iOS 16.0, *) {
            for case let scene as UIWindowScene in UIApplication.shared.connectedScenes {
                scene.requestGeometryUpdate(.iOS(interfaceOrientations: mask))
            }
        } else {
            let orientation: UIInterfaceOrientation = mask == .portrait ? .portrait : .landscapeRight
            UIDevice.current.setValue(orientation.rawValue, forKey: "orientation")
            UIViewController.attemptRotationToDeviceOrientation()
        }
    }
}
