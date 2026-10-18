# Audio and Video Porting Status

## Audio data and runtime path

[`internal/media/ffmpeg`](../internal/media/ffmpeg) is the decoder for WAVE, Ogg/Vorbis, ASF/WMV/WMA, MPEG-1/2, and other formats enabled by the deployed FFmpeg libraries. It uses purego bindings with a bounded memory-backed AVIO reader, converts audio to interleaved signed-16 PCM for `WaveSoundBuffer`, and converts video to RGBA. A missing or ABI-incompatible sidecar is reported once and audio/video opens become no-ops. `runtime.SoundStream` and `WaveSoundBuffer` preserve source-rate transport and SLI loop/label state. The runtime mixer produces bounded 48 kHz stereo float32 data for session extraction/frame-sink delivery.

The script-native `WaveSoundBuffer` in [`sound_native.go`](../internal/runtime/sound_native.go) includes storage/`openBytes`, transport, seek, fade, properties, and deterministic event delivery. This is headless state and sample generation; it does not itself open an OS audio device.

TCWF0 sources are detected by the `TCWF0\x1a` signature and decoded in-process by `internal/assets/wutcwf.go` before the FFmpeg path. The decoder covers mono/stereo MS+IMA ADPCM blocks and peak corrections; TCWF carries no loop metadata.

Linking `getSample.dll` installs the source-backed `WaveSoundBuffer.sampleValue`, `sampleCount`, and `sampleAhead` properties plus the static `setDefaultCounts(count)` and `setDefaultAheads(ahead)` methods. Defaults are 100 frames and 0 frames ahead. Reading `sampleValue` enables `useVisBuffer` and returns `(peak / 32768)^2` from the configured window of decoded PCM on channel 1, starting at the current source frame plus `sampleAhead`; sampling does not advance or seek playback. `sampleCount` must be non-negative. The legacy `WaveSoundBuffer.getSample([n])` function is not exposed: that implementation calls `getVisBuffer(buffer, n, 1)` and averages only non-negative samples, while this runtime has no native `getVisBuffer` callback/buffer contract to reproduce that behavior faithfully.

## Video path

`VideoOverlay` in [`video.go`](../internal/runtime/video.go) provides bounded source metadata, playback/event state, frame queues, and native-rate audio queues. Supported paths include AMV frame handling, FFmpeg-backed ASF/WMV/WMA, MPEG-1/2, and other enabled formats. FFmpeg video frames are converted to RGBA and queued with bounded ownership; decoded movie audio is mixed through the shared 48 kHz stereo mixer.

Non-AJPM sources are opened through the FFmpeg shared-library sidecar. `VideoOverlay.Open` selects the first decodable video and audio streams, reports stream metadata, converts video frames to RGBA, and queues bounded audio blocks. Decoder errors are returned with the source name and operation context. When the sidecar is missing or ABI-incompatible, the first failure is logged once and the open, transport, and seek calls become no-ops so the runtime keeps running. AlphaMovie (`AJPM`) sources remain on the repository decoder in [`assets/amv.go`](../internal/assets/amv.go).

The runtime requires `avutil`, `avcodec`, `avformat`, and `swscale` from the deployed FFmpeg 9.0.2 ABI. The loader searches `KRKRGO_FFMPEG_DIR`, `lib/ffmpeg`, and the executable directory; the deployment workflow installs the libraries beside the application.

The current video model retains headless transport and frame/audio queue semantics. Audio-stream selection beyond the first decodable stream, movie color/effect controls, and OS video-device parity remain outside this runtime contract.

## Device output

[`host.AudioOutput`](../internal/host/audio.go) is an opt-in adapter for runtime's fixed 48 kHz stereo float32 output. The caller creates an Ebitengine `audio.Context`, passes it to `host.NewAudioOutput`, and attaches the output to `host.Game`. The adapter has a bounded queue; the default `krkrgo run` and headless session do not create or activate this device-output path. Oto is an indirect dependency; platform details are in [Host and platform](host-platform.md).

## C++ comparison and gaps

Current WAVE data omits C++ `WaveIntf` fields such as speaker mask, total sample/time fields, and seekability. Video/audio selection and processing semantics beyond the bounded routes above remain unavailable. Unsupported codecs and device operations are reported rather than presented as supported.

**C++ reference:** [Kirikiri Z source](https://github.com/krkrz/krkrz), including its sound and video subsystems.
