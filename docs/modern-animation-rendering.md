# 现代动画与渲染架构报告

## 文档定位

本文讨论 `krkrgo` 面向现代设备的图形动画、资源加载、多核调度和 GPU 渲染方向。目标场景是游戏通过 TJS、KAG 和 Layer 系统驱动的自定义图形动画，包括图层移动、缩放、旋转、透明度变化、场景切换、规则过渡、mask、province 和脚本回调。

本文属于架构研究报告，不记录已完成的代码修改，也不宣称已有运行时性能结果。当前结论来自 Go 实现、官方 Kirikiri C++ 实现和 `krkrz_rs` 的源码对照。官方实现承担语义和兼容基线；现代性能方案应当使用新的数据布局、资源流水线、任务调度和 GPU 执行模型。

## 核心结论

整个引擎无需全量重写。TJS、KAG、Layer 公共 API、事件顺序、回放时钟和 CPU fallback 可以继续承担兼容层职责。需要重构的是图形执行层：资源加载、图像所有权、动画采样、transition 中间结果、CPU 像素任务和 GPU 提交协议。

当前 Go 路径把脚本更新、图层状态更新、资源加载、图像处理和画面捕获放在同一个 Update 生命周期中。大型图像加载时，读取、解码、颜色处理、mask/province 处理和 `SetRGBA` 复制都会占用动画更新线程。自定义动画状态因此无法按正常节奏推进。

现代架构应当把脚本语义和图形执行分成两个层次。脚本层继续保持顺序和可观察语义；资源准备、纯数值动画采样、图像预处理和 GPU 合成进入独立流水线。主线程负责脚本回调、状态提交、事件顺序和确定性时钟，worker 负责无副作用计算，GPU 负责高频像素处理。

## 当前实现与参考项目

`internal/runtime/session.go` 中的 `AdvanceClock` 和 `TickForPresentation` 依次执行定时器、调度器、连续处理器、脚本世界更新、Layer transition、事件派发、画面捕获和完成回调。该顺序对 Kirikiri 语义很重要，但其中的大量图形工作仍然同步执行。

`internal/runtime/layer_load_images_native.go` 的 `Layer.loadImages` 会同步读取 Storage、解码图像、处理 color key、读取 mask/province sidecar，并把结果提交给 Layer。`internal/runtime/layer.go` 的 `SetRGBA` 会复制完整 RGBA 数据。`internal/runtime/layer_draw.go` 和 compositor 仍包含 CPU 像素循环，transition 还可能同时保留源图像、目标图像和中间像素。

官方 C++ 项目已经提供两个有用的历史参考。`reference/krkrz/visual/GraphicsLoadThread.cpp` 实现了 `tTVPAsyncImageLoader`，通过请求队列、加载线程、完成队列和主线程事件处理支持 `Bitmap.loadAsync`。`reference/krkrz/visual/LayerBitmapIntf.cpp` 使用 `TVPBeginThreadTask`、`TVPExecThreadTask` 和 `TVPEndThreadTask` 对填充、混合、mask、复制、缩放和 affine 操作进行按区域并行处理。`reference/krkrz/utils/win32/ThreadImpl.cpp` 提供绘制任务线程管理，`drawThreadNum` 控制线程数量。

`reference/krkr2-main/cpp/core/visual/RenderManager.cpp` 延续了按区域分片的绘制模型。`reference/krkr2-main/cpp/core/impl/ThreadImpl.cpp` 提供新的线程任务接口。`reference/krkrz_rs` 则展示了另一组有价值的设计：Layer 图像共享、写入时分离、视觉状态变化检测、dirty rectangle、transition 状态管理和 wgpu 持久纹理。

这些项目说明了兼容语义和并行图形处理可以共存。现代设计应当进一步利用 GPU、数据导向布局、持久资源、预编译动画轨道和任务图。

## 目标架构

推荐的总体流水线如下：

```text
Storage IO
  → 解压与格式识别
  → 后台图像解码
  → mask/province/color-key 预处理
  → CPU decoded cache
  → GPU upload queue
  → resident texture

TJS/KAG 脚本
  → 语义事件与状态修改
  → 动画轨道采样任务
  → Layer visual state 提交
  → Render graph
  → GPU command submission
```

每个阶段都应当拥有明确的数据所有权、优先级、取消状态和完成状态。后台阶段生成不可变结果，主线程在确定的逻辑 tick 中提交结果。资源任务不直接访问 TJS VM、Layer 树或脚本对象。

推荐把资源状态表示为 `Requested`、`Loading`、`Decoded`、`Prepared`、`Uploading`、`Resident` 和 `Failed`。资源请求需要支持优先级、重复请求合并、取消、错误传播、CPU 内存配额、GPU 内存配额和场景预取。当前画面即将使用的资源应当获得最高优先级，远期场景预取使用较低优先级。

## 资源加载与动画流畅度

同步 `Layer.loadImages` 的语义应当继续存在，因为部分脚本会依赖调用完成后图像立即可见。现代资源系统应增加独立的异步接口或预加载接口。预加载请求在后台执行，完成后由主线程在固定 tick 中交换 Layer 资源。

后台线程适合处理文件读取、XP3 解压、图像解码、颜色转换、mask/province 解码、mipmap 生成和平台纹理格式转换。主线程负责检查 Layer 是否仍然有效、验证请求版本、提交资源句柄和触发脚本回调。

资源请求需要使用代际或版本号。一个 Layer 在请求图片 A 后又请求图片 B 时，A 的晚到结果不能覆盖 B。取消任务可以减少无效工作；版本检查负责保证提交顺序。完成回调应当进入现有事件调度器，禁止 worker 直接调用 TJS。

CPU decoded cache 与 GPU resident cache 应当分别管理。CPU cache 适合保存尚未上传或需要 CPU fallback 的数据，GPU cache 适合保存当前场景高频使用的纹理。缓存条目需要保存字节成本、最后使用时间、引用计数和资源代际。

## 图像所有权与内存传输

当前 `SetRGBA` 的整块复制会把大型图像加载成本扩大一倍。内部可以增加不可变 `DecodedImage`、共享 `TextureResource` 和写入用 `MutableSurface` 三类资源。普通 Layer 只保存资源引用、尺寸和显示状态；`fillRect`、`copyRect` 等写操作才创建独立可写缓冲。

transition 应当保存源纹理句柄、目标纹理句柄、规则纹理句柄和进度参数。常见 crossfade 无需每帧生成完整中间 RGBA 图像。多个 Layer 可以共享同一纹理，Layer 移动、缩放和透明度变化只修改 visual state。

对于平台 GPU 支持的格式，资源导入阶段可以生成 ASTC、ETC2、BCn 或平台适配的压缩纹理。mask 可以使用 R8，province 或索引图可以使用 R8/RG8，减少内存和上传带宽。原始图像仍可保留给 CPU fallback 和编辑型操作。

## 动画运行时

大量自定义动画的计算形式接近数值轨道：位置、缩放、旋转、透明度、可见性和颜色随时间变化。每帧通过通用 TJS 属性查找执行这些操作，会产生 dispatch、字符串键处理、Value 转换和函数调用成本。

可以将稳定的动画表达转换为专用轨道：

```text
AnimationClip
  ├── position track
  ├── opacity track
  ├── scale track
  ├── rotation track
  └── event track
```

轨道数据使用连续数组保存，运行时批量采样。SoA 布局、预计算插值系数、整数 Layer ID、预解析属性目标和 SIMD 可以减少访问成本。动画采样结果先写入 worker 私有输出，再由主线程按稳定顺序合并。

TJS 继续负责条件、事件、动态函数、脚本对象和游戏状态。规则化的高频动画交给专用 evaluator。编译器可以在未来把部分 TJS/KAG 动画识别为动画 IR，再由 Go evaluator、生成代码或 JIT 执行。

动画轨道之间没有脚本副作用时，可以按 Layer 或 clip 分组并行采样。涉及 onPaint、onTransitionCompleted、Layer 树修改、动态属性访问和 TJS 回调时，仍由主线程顺序处理。并行计算应当产生结果，主线程负责提交结果。

## 多核任务图

worker pool 应当采用有界任务图，而非为每次动画创建 goroutine。任务可以分为资源任务、CPU 图像任务、动画采样任务和渲染准备任务。每项任务声明输入资源、输出资源、优先级和依赖。

典型任务图如下：

```text
资源读取
  → 图像解码
  → mask/province 处理
  → GPU 上传

脚本更新
  → 动画轨道采样
  → transform resolve
  → dirty region 计算
  → render item 生成
  → GPU 提交
```

任务粒度需要根据图像大小和处理成本自适应。过小任务会被调度开销吞掉收益；过大任务会增加单帧尾延迟。图像逐行处理适合按 scanline 分区，任务之间写入互不重叠的像素范围。

worker 不能直接修改共享 Layer 状态。每个 worker 输出不可变结果或带版本号的结果包，主线程在 tick 中统一交换。这一规则同时服务于线程安全、确定性回放和调试。

## GPU 优先渲染

GPU 后端应当把可见 Layer 转换成紧凑的 render item：纹理句柄、transform、opacity、blend mode、clip rect、z-order 和 transition 参数。静态像素上传一次，动画期间更新 instance data。

多个 Layer 可以按纹理格式、shader、blend mode 和 sampler state 进行批处理。现代 GPU 路径可以使用 instanced quad、texture array、bindless texture、持久 staging buffer、ring buffer、indirect draw 和 render pass 合并。具体功能取决于 wgpu 后端，但 render item 模型可以先独立于具体 GPU 特性建立。

普通 crossfade、universal transition 和 ripple 可以改为 shader 参数驱动。shader 接收源纹理、目标纹理、规则纹理、进度和 vague 等参数，GPU 直接生成过渡结果。blur、displacement 和部分颜色处理可以使用 fragment shader、compute shader 或 ping-pong render target。

CPU compositor 仍应保留。它用于 deterministic replay、无 GPU 环境、调试、兼容性路径和特殊 Layer 操作。GPU 路径成为常规显示路径后，CPU 路径可以继续作为结果校验和 fallback。

## 脏区域与渲染图

当前性能文档已经记录了静态场景缓存、changed bounds 和部分上传能力。现代方案可以进一步把每帧工作分成内容变化和状态变化。

内容变化包括新图片、fillRect、copyRect、文字绘制和动态 paint。状态变化包括位置、透明度、缩放、旋转、裁剪和层级。状态变化通常只需要更新 render item；内容变化才需要刷新纹理或 CPU surface。

Render graph 可以根据 dirty region 生成最小必要 pass。普通移动图层可以更新受影响的旧区域和新区域；完整屏幕过渡、ripple 和带子节点的复杂 transition 仍可能需要完整 render target。每个 pass 应当记录输入纹理、输出纹理、尺寸、格式和 GPU 同步要求。

## JIT、AOT 与图形专用 IR

TJS 原生 JIT 适合作为长期方向。第一步应当是准备私有紧凑 bytecode、调用点元数据、热点计数和可复用的 prepared program。第二步可以生成动画 IR、批量 Layer 操作和数值循环专用代码。原生 JIT 需要处理 Go ABI、可执行内存、GC root map、异常恢复、预算、trace 和解释器回退，因此应当先服务少量稳定热点。

更现实的 AOT 形式是项目级准备缓存：源代码或 TJS2 字节码经过验证后生成紧凑指令、常量表、调用点描述、动画轨道、资源清单和预取计划。缓存键包含源内容哈希、编译器版本、语义版本、格式版本和影响代码生成的功能开关。

一个完整的场景预编译产物可以包含：预解析脚本、预构建动画 clip、预计算资源依赖、GPU 纹理格式信息、transition rule 清单和首屏预取计划。这样能降低启动期解析与资源发现成本。

## 帧调度

逻辑时间、动画采样和显示提交需要分开。逻辑时钟继续保持固定和可重放；动画采样使用逻辑时间；显示线程按平台节奏提交最新完成 frame；资源任务使用每帧预算。

当某个资源任务无法在当前显示周期内完成时，系统应当继续推进逻辑状态，提交最新可用 frame，并把资源任务延后到下一次预算。对于可跳过的中间动画状态，可以直接采样当前时间，减少长时间停顿带来的连续追赶。

主线程应记录 Update、动画采样、资源提交、CPU 合成、GPU upload、render pass 和 present 相关耗时。性能重点应放在 p99/p999 Update 时长、资源造成的最长停顿、首个动画帧时间、GPU upload 峰值和活跃纹理内存。

## 兼容性策略

公共 API 和脚本语义继续使用当前模型。新架构通过内部资源句柄、动画 evaluator、render item 和 GPU backend 接入。旧 API 可以生成传统同步操作，新 API 生成异步资源请求和专用动画任务。

CPU fallback 与 GPU 路径应当共享 Layer visual state 和资源版本。这样可以使用相同的脚本输入驱动两条渲染路径。deterministic replay 继续采用固定 tick 和稳定事件顺序；GPU 画面比较可以使用容差或离线参考图，具体校验策略单独定义。

资源提交需要遵守版本号、主线程交换和失败传播规则。transition 完成回调、onPaint、事件派发和 TJS 异常继续由现有 Session 顺序处理。性能优化不能改变脚本可观察的事件顺序。

## 推荐演进顺序

第一阶段建立资源管线：后台读取、解码、mask/province 处理、请求优先级、重复请求合并、主线程提交和 CPU decoded cache。该阶段直接针对大型资源加载造成的动画卡顿。

第二阶段建立资源句柄和图像共享：Layer 引用不可变图像资源，写操作使用独立 surface，transition 使用资源句柄，减少 `SetRGBA` 整块复制。

第三阶段建立动画 IR 和批量采样：稳定的 TJS/KAG 动画转换为 clip 和 track，worker 并行采样，主线程统一合并 visual state。

第四阶段建立 GPU render item 与 GPU transition：静态纹理持久驻留，普通 transform 使用实例数据，crossfade、universal 和 ripple 优先迁移到 shader。

第五阶段建立 AOT 准备缓存和场景预取：缓存紧凑 bytecode、动画轨道、资源依赖和纹理格式信息。

第六阶段再评估 JIT：只针对经过真实 workload 证明高占用的 TJS 数值循环、动画 evaluator 和稳定调用点生成机器码。

## 风险与取舍

资源异步化会引入完成顺序、取消、错误传播和脚本 API 语义，需要版本号和主线程提交协议。GPU 路径会引入纹理生命周期、上传队列、shader 版本、设备能力差异和像素结果差异。多核动画采样会增加数据同步和调试复杂度，因此应当限制在无副作用的数值计算。

最重要的取舍是保留可靠的兼容路径，同时为现代路径建立清晰的资源和状态协议。现代性能能力应当通过内部后端和新 API 增加，不需要让所有脚本迁移到新模型。

## 最终判断

当前项目不需要一次性重写 TJS、KAG 和 Layer 兼容层。需要一次图形执行层升级：异步资源流水线、共享图像资源、多核任务图、动画专用 IR、GPU render item、shader transition 和场景级预取。

官方 C++ 实现已经证明异步图片加载和多线程像素任务具有兼容可行性；现代设计可以继续向数据导向布局、GPU 优先合成、持久纹理、批量动画采样和 AOT 预处理推进。性能目标应当从“让串行 CPU 路径更快”提升为“让主线程只处理语义和提交，让资源与图形工作进入并行流水线”。

## 当前十项优化状态

### 异步资源读取、解码与预取（跳过）

当前 `Session`、`Storage`、TJS VM 和 layer API 使用同步资源结果与确定性 tick 顺序。异步管线需要新增任务生命周期、取消、优先级、CPU/GPU 配额和主线程提交协议；`amairo` 当前回放没有独立的资源切换耗时场景可用于测量收益。本项保留为架构方向，跳过运行时代码。

## 参考源码

- Go runtime：`internal/runtime/session.go`、`internal/runtime/layer.go`、`internal/runtime/layer_load_images_native.go`、`internal/runtime/layer_draw.go`、`internal/runtime/compositor.go`、`internal/assets/image_cache.go`。
- Go host：`internal/host/ebiten.go`。
- 官方 C++：`reference/krkrz/visual/GraphicsLoadThread.cpp`、`reference/krkrz/visual/LayerBitmapIntf.cpp`、`reference/krkrz/utils/win32/ThreadImpl.cpp`、`reference/krkrz/visual/LayerIntf.cpp`、`reference/krkrz/visual/win32/BasicDrawDevice.cpp`。
- `krkr2-main`：`reference/krkr2-main/cpp/core/visual/RenderManager.cpp`、`reference/krkr2-main/cpp/core/visual/LayerBitmapIntf.cpp`、`reference/krkr2-main/cpp/core/impl/ThreadImpl.cpp`。
- Rust：`reference/krkrz_rs/crates/krkrz_runtime/src/layer.rs`、`reference/krkrz_rs/crates/krkrz_runtime/src/layer/draw.rs`、`reference/krkrz_rs/crates/krkrz_runtime/src/transition.rs`、`reference/krkrz_rs/crates/krkrz_cli/src/native_host.rs`。
- 相关现状：`docs/performance.md`、`docs/graphics-composition.md`、`docs/tjs-optimization.md`、`docs/tjs-memory-optimization.md`。
