import Foundation
import Krkrgo

final class SecurityScopedStorage: NSObject, KRKRGOContentURIReader {
    let root: URL

    private struct OpenFile {
        let url: URL
        let handle: FileHandle
    }

    private let lock = NSLock()
    private var nextHandle: Int64 = 1
    private var openFiles: [Int64: OpenFile] = [:]
    private var isScoped = false

    init(bookmark: Data) throws {
        var isStale = false
        root = try URL(
            resolvingBookmarkData: bookmark,
            options: [.withoutUI],
            relativeTo: nil,
            bookmarkDataIsStale: &isStale,
        ).standardizedFileURL
        if isStale {
            throw ProjectImportError.staleBookmark
        }
        super.init()
    }

    func start() throws {
        if isScoped {
            return
        }
        guard root.startAccessingSecurityScopedResource() else {
            throw CocoaError(.fileReadNoPermission)
        }
        isScoped = true
    }

    func close() {
        lock.lock()
        let files = Array(openFiles.values)
        openFiles.removeAll()
        lock.unlock()
        for file in files {
            try? file.handle.close()
        }
        if isScoped {
            root.stopAccessingSecurityScopedResource()
            isScoped = false
        }
    }

    func close(_ handle: Int64) {
        lock.lock()
        let file = openFiles.removeValue(forKey: handle)
        lock.unlock()
        try? file?.handle.close()
    }

    private func openHandle(_ path: String?) throws -> Int64 {
        let url = try url(for: path)
        let handle = try FileHandle(forReadingFrom: url)
        lock.lock()
        defer { lock.unlock() }
        let id = nextHandle
        nextHandle += 1
        openFiles[id] = OpenFile(url: url, handle: handle)
        return id
    }

    func open(_ path: String?, ret0_: UnsafeMutablePointer<Int64>?) throws {
        ret0_?.pointee = try openHandle(path)
    }

    private func sizeValue(_ handle: Int64) throws -> Int64 {
        let file = try openFile(handle)
        let attributes = try FileManager.default.attributesOfItem(atPath: file.url.path)
        return (attributes[.size] as? NSNumber)?.int64Value ?? 0
    }

    func size(_ handle: Int64, ret0_: UnsafeMutablePointer<Int64>?) throws {
        ret0_?.pointee = try sizeValue(handle)
    }

    private func readData(_ handle: Int64, offset: Int64, size: Int64) throws -> Data {
        guard offset >= 0, size >= 0, size <= Int64(Int.max) else {
            throw CocoaError(.fileReadInvalidFileName)
        }
        lock.lock()
        defer { lock.unlock() }
        guard let file = openFiles[handle] else {
            throw CocoaError(.fileReadUnknown)
        }
        try file.handle.seek(toOffset: UInt64(offset))
        return try file.handle.read(upToCount: Int(size)) ?? Data()
    }

    func read(_ handle: Int64, offset: Int64, size: Int64) throws -> Data {
        try readData(handle, offset: offset, size: size)
    }

    func list(_ path: String?, error: NSErrorPointer) -> String {
        do {
            let directory = try url(for: path)
            let entries = try FileManager.default.contentsOfDirectory(
                at: directory,
                includingPropertiesForKeys: [.isDirectoryKey],
                options: [.skipsHiddenFiles],
            )
            return try entries
                .sorted { $0.lastPathComponent.localizedStandardCompare($1.lastPathComponent) == .orderedAscending }
                .map { entry in
                    let values = try entry.resourceValues(forKeys: [.isDirectoryKey])
                    return (values.isDirectory == true ? "D" : "F") + entry.lastPathComponent
                }
                .joined(separator: "\u{0}")
        } catch let caughtError {
            error?.pointee = caughtError as NSError
            return ""
        }
    }

    private func statValue(_ path: String?) throws -> Int64 {
        let url = try url(for: path)
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: url.path, isDirectory: &isDirectory) else {
            throw CocoaError(.fileNoSuchFile)
        }
        if isDirectory.boolValue {
            return -1
        }
        let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
        return (attributes[.size] as? NSNumber)?.int64Value ?? 0
    }

    func stat(_ path: String?, ret0_: UnsafeMutablePointer<Int64>?) throws {
        ret0_?.pointee = try statValue(path)
    }

    func mkdir(_ path: String?) throws {
        let url = try url(for: path)
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
    }

    func write(_ path: String?, offset: Int64, data: Data?) throws {
        guard offset >= 0 else {
            throw CocoaError(.fileWriteInvalidFileName)
        }
        let url = try url(for: path)
        guard url != root else {
            throw CocoaError(.fileWriteInvalidFileName)
        }
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        if !FileManager.default.fileExists(atPath: url.path) {
            FileManager.default.createFile(atPath: url.path, contents: nil)
        }
        let handle = try FileHandle(forWritingTo: url)
        defer { try? handle.close() }
        try handle.seek(toOffset: UInt64(offset))
        try handle.write(contentsOf: data ?? Data())
    }

    func delete(_ path: String?) throws {
        let url = try url(for: path)
        guard url != root else {
            throw CocoaError(.fileWriteInvalidFileName)
        }
        try FileManager.default.removeItem(at: url)
    }

    private func openFile(_ handle: Int64) throws -> OpenFile {
        lock.lock()
        defer { lock.unlock() }
        guard let file = openFiles[handle] else {
            throw CocoaError(.fileReadUnknown)
        }
        return file
    }

    private func url(for path: String?) throws -> URL {
        let value = (path ?? "").replacingOccurrences(of: "\\", with: "/")
        var url = root
        for component in value.split(separator: "/", omittingEmptySubsequences: true) {
            let name = String(component)
            guard name != ".", name != "..", !name.contains("\0") else {
                throw CocoaError(.fileReadInvalidFileName)
            }
            url.appendPathComponent(name, isDirectory: false)
        }
        return url.standardizedFileURL
    }
}
