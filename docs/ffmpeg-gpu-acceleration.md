# FFmpeg GPU 解码与加速参考

## 范围与术语

本文整理 mpv、VLC、Kodi 与 FFmpeg 官方资料，服务于 krkrgo 后续媒体路径设计。资料查询日期：2026-10-13。本文只记录公开资料描述的架构与接口，没有对本机 GPU、驱动或播放器进行实测。

“硬件解码”指压缩视频包经平台解码器转换为视频帧，入口通常是 FFmpeg 的硬件设备/硬件帧上下文，或播放器对这些接口的封装。“GPU 合成/滤镜”指解码后的帧在 GPU 上进行缩放、色彩转换、去隔行、锐化、叠加、色调映射或最终呈现。两者可分别启用：软件解码配 GPU 滤镜，硬件解码配 CPU 滤镜，也可让解码帧全程保留在 GPU 资源中。

FFmpeg 的命令行文档把解码器描述为“压缩包到原始帧”，把 filtergraph 描述为处理原始音视频帧的处理图：[ffmpeg Documentation](https://ffmpeg.org/ffmpeg.html#Detailed-description)。这一区分适用于下文所有项目比较。

## 项目对照

| 项目 | 硬件解码入口 | FFmpeg API / 后端 | 解码帧位置 | 滤镜与合成路径 | 回退策略 | 对 krkrgo/Ebitengine 的直接启示 |
| --- | --- | --- | --- | --- | --- | --- |
| **mpv** | `--hwdec=auto` 或指定后端；`--hwdec-codecs` 控制编解码器范围 | mpv 的 FFmpeg 解码器与平台互操作层；`d3d11va`、`vaapi`、`nvdec`、`videotoolbox`、`vulkan` 等 | 非 `-copy` 模式优先保持硬件帧并交给 `--vo=gpu`/`--vo=gpu-next`；`*-copy` 明确复制到系统内存 | `gpu` 与 `gpu-next` 使用 GPU 输出、缩放、色彩处理、HDR/色调映射及可配置着色器；视频滤镜写入新帧时可能使 direct-rendering 路径失效 | 自动模式按可用后端尝试，初始化失败时回到软件解码；`--hwdec-software-fallback` 可按连续失败帧数触发回退 | 将“硬解、帧驻留、滤镜、显示后端”作为独立选择；优先保留软件 RGBA 路线，未来可增加硬件帧到 Ebitengine 的专用呈现路线 |
| **VLC** | 偏好设置中的硬件加速；官方页面列出 Windows DXVA、Linux VA-API/VDPAU、macOS VDA 等入口 | VLC 的模块化解码器，页面说明 VA-API 路径使用 FFmpeg/VA-API；平台后端由模块提供 | 官方 VA-API 页面描述的模块化路线在解码阶段使用 GPU，随后把数据取回系统内存以供后续模块使用 | 取回系统内存后可接任意视频输出和 VLC 视频滤镜；另一份官方 GPU 页面说明部分平台可由显卡承担后处理与渲染 | 硬件能力受编解码器、显卡与系统影响；播放器偏好设置控制启用状态，软件解码可作为可用路径 | 若后续要保持 CPU 合成和现有帧队列，VLC 的“硬解后取回 CPU 帧”模型更接近当前接口；GPU 合成需要单独的纹理互操作设计 |
| **Kodi** | 视频设置中的 DXVA2、VAAPI、VDPAU、VTBDecoder 等平台选项 | Kodi 播放器封装各平台视频解码接口；官方设置页按 Windows、Linux、macOS 分列 | 设置页将硬件解码与渲染方式分开；具体帧驻留取决于平台解码器、渲染器与格式转换路径 | “Render method”、HQ scaler、10 位处理、DXVA Video Super Resolution 等选项体现渲染与滤镜阶段；UI 本身也依赖图形 API | 设置页为每个平台提供独立开关，未启用对应硬件路径时由 CPU 解码/处理；驱动与硬件能力决定可用性 | 设计配置时把解码能力探测、帧格式、缩放和合成分别记录；Ebitengine 当前上传 RGBA，先维持 CPU 帧合同更易保持跨平台一致性 |
| **FFmpeg** | `-hwaccel`、`-init_hw_device`、`-hwaccel_device`；库调用 `av_hwdevice_ctx_create` | `AVHWDeviceContext`、`AVHWFramesContext`；设备类型包含 VAAPI、CUDA、DXVA2、D3D11VA、VideoToolbox、Vulkan、QSV 等 | `AVHWFramesContext` 管理硬件帧池；`av_hwframe_transfer_data` 在硬件表面与系统内存间传输；`hwmap` 支持映射到系统内存或另一设备 | `hwupload` 把系统内存帧上传到硬件表面，`hwdownload` 下载硬件帧；CUDA、VAAPI、Vulkan、D3D 等滤镜可组成 GPU 处理图 | `-hwaccel auto` 只选择运行时可用且解码器支持的方式；FFmpeg 文档明确指出硬件方法可用性依赖构建、硬件和驱动 | 未来若启用硬件解码，需同时管理设备上下文、硬件帧池、格式转换、传输时机和资源生命周期；FFmpeg 的硬件帧可作为内部媒体层合同，外层仍可输出 RGBA |

表中“帧位置”代表官方接口定义的典型路径，具体结果仍取决于编解码器、驱动、像素格式、渲染后端和滤镜图配置。

## mpv：硬解与 GPU 输出紧密配合

mpv 官方手册的 `--hwdec` 选项支持 `auto`、指定 API 与 `no`；硬件解码无法使用时回到软件解码。手册列出 `d3d11va`、`videotoolbox`、`vaapi`、`nvdec`、`drm`、`vulkan` 等后端，并区分 `d3d11va-copy`、`videotoolbox-copy`、`vaapi-copy`、`nvdec-copy` 等系统内存复制模式：[mpv Reference Manual，Options](https://mpv.io/manual/stable/#options)。

mpv 将硬件解码与视频输出后端关联：部分硬解要求 `--vo=gpu` 或 `--vo=gpu-next`，Vulkan 视频解码要求 `gpu-next`。`gpu-next` 基于 libplacebo，`gpu` 是可定制的 GPU 视频输出；两者负责纹理采样、缩放、色彩和呈现：[mpv Reference Manual，Video output drivers](https://mpv.io/manual/stable/#video-output-drivers)。这体现了“解码设备”和“呈现设备”之间需要互操作协议，单独打开硬件解码并不能自动得到 GPU 合成收益。

mpv 手册还说明：`--hwdec-software-fallback` 可在硬件解码连续失败时启用软件解码；`--vd-lavc-dr` 的 direct rendering 只有部分视频输出支持，写入图像数据或产生新帧的滤镜会关闭该路径；`*-copy` 模式则主动把帧复制回系统内存：[mpv Reference Manual，Hardware decoding options](https://mpv.io/manual/stable/#options)。

对 krkrgo 的参考价值是把策略分为三个层次：解码器选择、帧存储位置、输出/滤镜后端。当前 Ebitengine 路径以 CPU RGBA 帧为输入，未来可先增加硬件解码能力探测与显式 `copy` 路径，再评估纹理互操作。

## VLC：模块化路径强调可组合性

VideoLAN 官方 GPU 解码页面说明，VLC 可按编解码器、显卡和操作系统使用图形芯片加速解码；部分情况下显卡也可承担后处理和渲染：[VLC GPU Decoding](https://wiki.videolan.org/VLC_GPU_Decoding)。该页面同时列出 Windows 的 DXVA、Linux 的 VA-API/VDPAU、macOS 的 VDA 等平台入口。

VLC 官方 VA-API 页面给出了更具体的模块化描述：GPU 用于解码阶段，之后数据取回系统内存，以便继续进行流处理、滤镜或连接任意视频输出；这条路径可能产生 GPU 到 CPU 的传输成本，但可保留 VLC 后续模块的组合能力：[VLC VAAPI](https://wiki.videolan.org/VLC_VAAPI)。该页面最后编辑日期为 2019-02-09，本文将其作为架构说明，具体平台能力以当前 VLC 与驱动文档为准。

VLC 对 krkrgo 的启示是：如果播放目标包含 CPU 合成、脚本图层和截图，硬件解码后显式取得 CPU 可读帧是一条清晰路径；如果目标改为 GPU 视频与图层合成，就需要让解码模块直接交付可被渲染器采样的资源，同时为字幕、截图和软件滤镜提供显式传输路径。

## Kodi：解码开关与渲染选项分层

Kodi 官方视频设置页面把硬件解码选项按平台列出：Windows 有 DXVA2，Linux 有 VDPAU 与 VAAPI，macOS 有 VTBDecoder；同一页面另列 Render method、HQ scaler、10 位高精度处理和 DXVA Video Super Resolution：[Settings/Player/Videos](https://kodi.wiki/view/Settings/Player/Videos)。页面标注为 v21 内容。

这些选项说明 Kodi 将“是否使用硬件解码”和“如何处理、缩放、显示视频”分别呈现给用户。设置页还说明，部分硬件超分依赖特定 GPU 与驱动设置，并可能增加 GPU 功耗；硬件能力、驱动和输入格式共同决定实际路径。

Kodi 官方硬件页面说明其界面使用 OpenGL/OpenGL ES 或 Windows DirectX，视频硬件解码能力依赖平台可用的 VAAPI、VDPAU 等接口：[Supported hardware](https://kodi.wiki/view/Supported_hardware)。该页面内容标注为 v17，平台要求部分可能随版本变化，因此这里只采用其“图形界面与视频解码能力分别依赖图形接口和视频接口”的结构性信息。

对 krkrgo 的直接启示是维护独立状态：`decoder backend`、`decoded pixel format`、`frame residency`、`scaler/filter backend` 与 `present backend` 分开描述。这样可以让 Ebitengine 继续消费稳定的 RGBA 帧，也为将来使用纹理或硬件表面保留选择空间。

## FFmpeg：设备、硬件帧与传输 API

FFmpeg 的 `libavutil/hwcontext.h` 官方 API 定义了 `AVHWDeviceContext`、`AVHWFramesContext` 和 `AVHWFramesConstraints`。设备类型枚举包含 VDPAU、CUDA、VAAPI、DXVA2、QSV、VideoToolbox、D3D11VA、Vulkan、D3D12VA 等；`av_hwdevice_ctx_create` 创建设备上下文，`av_hwframe_ctx_alloc` 创建与设备关联的硬件帧上下文，`av_hwframe_get_buffer` 从硬件帧池取得帧：[FFmpeg libavutil/hwcontext.h](https://ffmpeg.org/doxygen/trunk/hwcontext_8h.html)。

`av_hwframe_transfer_data` 负责在硬件表面与另一帧之间传输数据，`av_hwframe_map` 负责映射硬件帧。API 文档将硬件帧描述为由硬件设备管理的帧池，并提供格式约束查询。应用因此需要处理设备初始化、帧池容量、像素格式、传输方向和引用计数。

FFmpeg 命令行文档中，`-hwaccel` 默认值为 `none`，`auto` 选择可用硬件加速方法；`-hwaccel_device` 选择设备；`-hwaccels` 列出构建中启用的硬件加速组件。文档指出，运行时可用性还依赖硬件与驱动，且所选方式必须获得对应解码器支持：[ffmpeg options，Hardware acceleration](https://ffmpeg.org/ffmpeg.html#Advanced-options)。

FFmpeg 滤镜文档把 GPU 传输操作单列为 `hwupload`、`hwdownload` 与 `hwmap`：`hwupload` 上传系统内存帧，`hwdownload` 下载硬件帧，`hwmap` 映射到系统内存或另一设备；文档还列出 CUDA、VAAPI、Vulkan 等硬件滤镜：[FFmpeg Filters Documentation，Hardware frame filters](https://ffmpeg.org/ffmpeg-filters.html#hwdownload)、[hwupload](https://ffmpeg.org/ffmpeg-filters.html#hwupload)、[hwmap](https://ffmpeg.org/ffmpeg-filters.html#hwmap)。

这组接口把“硬件解码”与“GPU 滤镜/合成”连接起来：硬件解码产生硬件帧；硬件滤镜可以继续消费硬件帧；需要 CPU 滤镜、截图或 CPU 合成时调用传输；需要渲染器消费时则保持硬件帧并建立设备互操作。

## krkrgo 当前状态与 Ebitengine 关联

当前项目文档记录的媒体路径仍采用软件解码构建方案：`internal/media/ffmpeg` 通过 purego 绑定 FFmpeg，视频帧转换为 RGBA 后进入有界队列；运行时要求 `avutil`、`avcodec`、`avformat` 与 `swscale` 的 FFmpeg 9.0.2 ABI。详见 [音视频移植状态](audio-video.md)。本文保持该构建方案与 GPU 代码现状。

当前图形路径由 CPU 合成 RGBA 快照，再通过 Ebitengine `WritePixels` 上传到 `ebiten.Image`，最后 `DrawImage` 呈现；详见 [图形合成：CPU 与 GPU](graphics-composition.md)。因此当前 GPU 主要承担最终图像呈现，视频解码和图层合成仍使用 CPU 帧模型。

后续设计可按以下顺序评估：

1. **先保持现有软件路径。** 软件解码、RGBA 转换、有界队列和 CPU 合成构成可理解的跨平台合同；当前构建继续按此方案发布。
2. **再增加能力探测记录。** 记录 FFmpeg 构建中的设备类型、解码器支持、驱动可用性、硬件像素格式和所需传输方向；能力缺失时沿用软件路径。
3. **优先验证“硬解后取回 CPU”。** 这与 VLC 的模块化说明接近，能继续复用现有 RGBA 队列、脚本图层、截图和 Ebitengine 上传路径，代价是硬件到系统内存传输。
4. **独立评估 GPU 合成。** 若视频帧与图层都转为 GPU 资源，需要确定 Ebitengine 是否能接收外部纹理或硬件表面，并处理字幕、截图、转场、色彩空间和设备丢失；这属于合成架构设计，不能由硬件解码开关单独解决。
5. **保留软件回退。** 解码器、驱动、像素格式、滤镜与渲染器任一环节能力不足时，回到当前软件解码和 RGBA 合成路径，保证媒体队列与脚本时序接口稳定。

### FFmpeg 硬件解码与 GPU 帧链路（跳过）

当前 sidecar 与 `internal/media/ffmpeg` 只提供软件解码、RGBA 转换和有界帧队列；项目没有已接入的 D3D11VA、VAAPI、VideoToolbox 或 CUDA 帧路径。缺少可运行的硬件帧合同和对应视频验收场景，无法取得端到端实测收益。本项跳过运行时代码。

## 结论

- 硬件解码降低压缩视频解码阶段的 CPU 工作量；GPU 合成与滤镜降低帧处理、缩放、色彩和呈现阶段的 CPU 工作量，两个目标需要分别设计。
- mpv 展示了“硬件帧直通 GPU 输出”与“`*-copy` 取回系统内存”的并列方案，并以软件解码作为自动初始化失败后的回退。
- VLC 的官方 VA-API 说明展示了“GPU 解码、CPU 后续模块”的组合方式，适合需要 CPU 合成与任意滤镜的场景；其 GPU 页面同时记录了部分平台的后处理和渲染能力。
- Kodi 将硬件解码开关、渲染方法、缩放和高精度处理分开配置，说明播放器需要显式表达解码、帧格式、滤镜和呈现阶段。
- FFmpeg 提供设备上下文、硬件帧池、映射和传输 API，但它不会替应用决定帧的最终归属；应用仍需设计资源生命周期、格式转换、滤镜图与回退路径。
- krkrgo 当前保持软件解码构建方案。近期设计重点应放在稳定的 RGBA 帧合同与能力探测记录；GPU 解码或 GPU 合成属于后续独立方案，当前文档不引入代码和构建改动。

## 官方资料索引

- mpv Reference Manual：<https://mpv.io/manual/stable/>（查询日期：2026-10-13）
- VLC GPU Decoding：<https://wiki.videolan.org/VLC_GPU_Decoding>（查询日期：2026-10-13）
- VLC VAAPI：<https://wiki.videolan.org/VLC_VAAPI>（查询日期：2026-10-13）
- Kodi Settings/Player/Videos：<https://kodi.wiki/view/Settings/Player/Videos>（查询日期：2026-10-13）
- Kodi Supported hardware：<https://kodi.wiki/view/Supported_hardware>（查询日期：2026-10-13）
- FFmpeg 命令行文档：<https://ffmpeg.org/ffmpeg.html>（查询日期：2026-10-13）
- FFmpeg `libavutil/hwcontext.h` API：<https://ffmpeg.org/doxygen/trunk/hwcontext_8h.html>（查询日期：2026-10-13）
- FFmpeg Filters Documentation：<https://ffmpeg.org/ffmpeg-filters.html>（查询日期：2026-10-13）
