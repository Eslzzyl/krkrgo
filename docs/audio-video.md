# Audio and Video Porting Status

## Audio data and runtime path

[`internal/assets/media.go`](../internal/assets/media.go) decodes bounded WAVE and Ogg/Vorbis data into interleaved signed-16 PCM. `runtime.SoundStream` and `WaveSoundBuffer` ([`audio.go`](../internal/runtime/audio.go), [`sound.go`](../internal/runtime/sound.go)) preserve source-rate transport and SLI loop/label state. The runtime mixer ([`audio_mixer.go`](../internal/runtime/audio_mixer.go)) produces bounded 48 kHz stereo float32 data for session extraction/frame-sink delivery.

The script-native `WaveSoundBuffer` in [`sound_native.go`](../internal/runtime/sound_native.go) includes storage/`openBytes`, transport, seek, fade, properties, and deterministic event delivery. This is headless state and sample generation; it does not itself open an OS audio device.

Linking `getSample.dll` installs the source-backed `WaveSoundBuffer.sampleValue`, `sampleCount`, and `sampleAhead` properties plus the static `setDefaultCounts(count)` and `setDefaultAheads(ahead)` methods. Defaults are 100 frames and 0 frames ahead. Reading `sampleValue` enables `useVisBuffer` and returns `(peak / 32768)^2` from the configured window of decoded PCM on channel 1, starting at the current source frame plus `sampleAhead`; sampling does not advance or seek playback. `sampleCount` must be non-negative. The legacy `WaveSoundBuffer.getSample([n])` function is not exposed: that implementation calls `getVisBuffer(buffer, n, 1)` and averages only non-negative samples, while this runtime has no native `getVisBuffer` callback/buffer contract to reproduce that behavior faithfully.

## Video path

`VideoOverlay` in [`video.go`](../internal/runtime/video.go) provides bounded source metadata, playback/event state, and frame queues. Supported paths include AMV frame handling and progressive MPEG-1/2 4:2:0 I-frame decode/presentation ([`amv.go`](../internal/assets/amv.go), [`mpeg2.go`](../internal/assets/mpeg2.go), [`mpeg2_decode.go`](../internal/assets/mpeg2_decode.go)). MPEG P/B pictures, interlaced pictures, and non-4:2:0 chroma are unsupported.

ASF/WMV is recognized by the ASF Header Object GUID signature or `.asf`/`.wmv` filename extension before MPEG parsing. The current implementation intentionally routes these sources to an empty placeholder: `VideoOverlay.Open` succeeds, transport operations are no-ops, and no source metadata, video frame, or audio block is produced. This keeps WMV assets from terminating the demo while real decoding remains pending. The placeholder is not WMV playback support and uses no FFmpeg process or native media library. In the extracted lovelyday project, `movie.xp3` contains `logo.wmv` and `opening.wmv`; `logo.wmv` contains WMV3 / Windows Media Video 9 video and WMA2 audio.

The lovelyday archive members and their ASF container signature have been directly verified with `CGO_ENABLED=0 go run ./cmd/krkrgo-tool extract --archive /Users/eslzzyl/Games/lovelyday/movie.xp3 --output /tmp/lovelyday-movie-extract-verify`. The extracted `logo.wmv` is 732,593 bytes and `opening.wmv` is 145,529,883 bytes. Each begins with the 16-byte ASF GUID `30 26 b2 75 8e 66 cf 11 a6 d9 00 aa 00 62 ce 6c`; `file` identifies the first as `Microsoft ASF ASF_Extended_Content_Description_Object` and the second as `Microsoft ASF`. A separate long-name protection notice in the extraction manifest was written as `__xp3_extract_manifest.json`; it did not prevent extracting either media member. These checks verify XP3 extraction and ASF identification, not that either video was playable.

During a limited logo skip replay, execution reached `macPlayMovie` and proceeded into the title, but the `MovieObj` frame, position, FPS, `numberOfFrame`, and `totalTime` all remained zero. Actual WMV decoding and frame/audio presentation were not verified. The official C++ WMV path uses `BuildWMVGraph` and DirectShow; recognizing the archive members and ASF signature in Go does not implement that playback path.

The native `VideoOverlay` currently reports zero audio streams and one video stream. Audio-stream selection, mixing-layer operations, and movie color/effect controls return unsupported errors. The current video model is not full C++ video-device parity.

## Device output

[`host.AudioOutput`](../internal/host/audio.go) is an opt-in adapter for runtime's fixed 48 kHz stereo float32 output. The caller creates an Ebitengine `audio.Context`, passes it to `host.NewAudioOutput`, and attaches the output to `host.Game`. The adapter has a bounded queue; the default `krkrgo run` and headless session do not create or activate this device-output path. Oto is an indirect dependency; platform details are in [Host and platform](host-platform.md).

## C++ comparison and gaps

Current WAVE data omits C++ `WaveIntf` fields such as speaker mask, total sample/time fields, and seekability. Video/audio selection and processing semantics beyond the bounded routes above remain unavailable. Unsupported codecs and device operations are reported rather than presented as supported.

**C++ reference:** [Kirikiri Z source](https://github.com/krkrz/krkrz), including its sound and video subsystems.
