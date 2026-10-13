# Porting Documentation

`krkrgo` incrementally migrates selected Kirikiri/Krkrz behavior to Go. Rust is a migration reference; the official C++ [Kirikiri Z project](https://github.com/krkrz/krkrz) is the semantic and acceptance target. The current source provides bounded surfaces, not complete engine parity.

Each domain guide links the relevant Go source and records current implementation and gaps against that C++ reference:

- [TJS and KAG](tjs-kag.md)
- [Assets, storage, and media](assets-storage-media.md)
- [Runtime and native services](runtime-natives.md)
- [Graphics, layers, and text](graphics-layer-text.md)
- [Audio and video](audio-video.md)
- [Graphics composition: CPU vs GPU](graphics-composition.md)
- [Host, Ebitengine, platforms, and dependencies](host-platform.md)
- [Verification and cross-cutting gaps](verification-and-gaps.md)
- [Launcher architecture](launcher.md)

Design notes that are not porting status:

- [Built-in translation](builtin-translation.md): where a dictionary-driven translation layer fits in the current text pipeline.

For launch and package navigation, see the [root README](../README.md). Recorded verification scope is in [Verification and cross-cutting gaps](verification-and-gaps.md).
