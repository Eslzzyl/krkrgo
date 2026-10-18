# 无头性能参考

## 文档定位

本文记录当前 Go 实现的无头性能测量结果，覆盖真实 `amairo` 项目回放、TJS、CPU 合成、host 上传准备、XP3 读取和图片路径。

无头回放使用固定逻辑时间。墙钟耗时包含项目启动、资源扫描、脚本执行、CPU 画面捕获和 replay frame 摘要生成。它适合比较同一环境下的变化，也适合定位 CPU、分配和内存压力。

Ebitengine 的窗口 Present、swap chain、VSync、GPU 队列和显示刷新不在这些结果中。交互显示仍需要独立的实窗测量。

## 渲染路径说明

无头 replay 与交互 GPU 呈现使用两条运行路径：

- `cmd/krkrgo-tool replay` 不启动 Ebitengine。它通过 `Session.Frame` 获取 CPU `FrameSnapshot`，因此会执行 CPU compositor 和 `drawLayerTo`。
- `krkrgo run -renderer gpu` 使用交互 GPU 路径。该路径捕获 RenderPlan、维护纹理缓存，并使用 Kage shader 执行支持的图层和 transition 操作。
- CPU compositor 仍服务于 headless replay、显式 CPU renderer 和 GPU 初始化失败后的 fallback。

因此本文的 `drawLayerTo`、`ComposeFullFrame` 和 CPU transition 数值描述无头 CPU 路径与 CPU fallback。它们不能用于排序交互 GPU 路径的 shader、纹理上传、GPU queue 或 Present 成本。交互 GPU 结论需要独立的实窗或 GPU 后端测量。

## 测量环境

- OS：Windows amd64
- CPU：Intel(R) Core(TM) Ultra 7 255H
- Go：go1.27.1
- 构建：`go build -trimpath ./cmd/krkrgo-tool`
- benchmark：`-benchtime=500ms -count=3 -benchmem`
- 回放 tick：16 ms
- 回放指令预算：100,000,000
- 项目：`testdata/amairo`
- `amairo` 文件总数：232
- `amairo` 文件总字节数：1,336,207,466
- `amairo` XP3 数量：14
- `amairo` XP3 总字节数：1,306,298,471

## 实际项目回放

### 输入

用于启动和动画测量的输入为：

```json
[{"at_ms":10000,"action":"checkpoint","name":"end","inspect":[]}]
```

该 checkpoint 让 replay 从启动后的 0 ms 推进到 10,000 ms，并在末尾记录状态。命令：

```sh
go build -trimpath -o temp/krkrgo-tool-perf.exe ./cmd/krkrgo-tool

printf '[{"at_ms":10000,"action":"checkpoint","name":"end","inspect":[]}]' | temp/krkrgo-tool-perf.exe replay \
  --project testdata/amairo \
  --input - \
  --output temp/perf-amairo-10s.json \
  --step-ms 16 \
  --budget 100000000
```

### 结果

| 场景 | 墙钟耗时 | 虚拟结束时间 | frame 数量 | 不同 RGBA 摘要数 | 结果状态 |
| --- | ---: | ---: | ---: | ---: | --- |
| 启动 + 1 s，第一次 | 50.34 s | 1,000 ms | 64 | 2 | 成功 |
| 启动 + 1 s，第二次 | 51.03 s | 1,000 ms | 64 | 2 | 成功 |
| 启动 + 10 s | 126.51 s | 10,000 ms | 626 | 270 | 成功 |

两个 1 s 输出文件的 SHA-256 完全相同：

```text
6cf00eea902de349bf781b2e442cde87e5d6f394956d5b4afec58607ca131969
```

每个 frame 为 1280×720，RGBA 字节数为 3,686,400。10 s 回放的 626 个 frame 对应初始 frame 加上 16 ms tick 产生的 625 个时间点。

### TJS2 Legacy 准备缓存

`DecodeBytecode` 为每个 Legacy object 保留一次性准备结果。首次执行时解析指令边界并建立 offset-to-IP 表，后续执行直接复用；公开的 `DecodeLegacyInstructions` 仍返回独立副本。

| 回放 | 缓存前 | 当前实现 | 墙钟变化 | 输出 |
| --- | ---: | ---: | ---: | --- |
| 启动 + 1 s | 15.535 s | 13.191 s | -15.1% | SHA-256 相同 |
| 启动 + 10 s | 126.510 s | 61.061 s | -51.7% | SHA-256 相同 |

1 s 对照使用本次同一命令重新运行。10 s 缓存前数值来自本文已有的同机基线；两次 JSON 文件 SHA-256 均保持一致：1 s 为 `6cf00eea902de349bf781b2e442cde87e5d6f394956d5b4afec58607ca131969`，10 s 为 `21ed220ff5d867bc40ae5d8e5d5bc831f1a5610c9be43735dfe4b8504d5a60fd`。

该改动针对完整 amairo replay 的 TJS2 Legacy 执行路径，收益覆盖启动和持续 tick。

可重复命令：

```sh
printf '[{"at_ms":1000,"action":"checkpoint","name":"end","inspect":[]}]' | temp/krkrgo-tool-perf.exe replay \
  --project testdata/amairo --input - --output temp/perf-amairo-1s.json \
  --step-ms 16 --budget 100000000
```

### Legacy 紧凑指令候选（跳过）

将运行时缓存指令从公开 `LegacyInstruction` 转为不保存名称字段的紧凑结构，并在错误路径按 opcode 查询名称。`amairo` 10 s 回放从缓存版本的 61.061 s 变为 63.111 s，输出 SHA-256 保持一致。该候选产生约 3.4% 倒退，代码已撤回，没有进入生产提交。


### 跨启动 TJS 准备缓存（跳过）

当前 `DecodedBytecode` 已在单次进程内缓存 Legacy 准备结果。跨启动缓存需要独立的版本、完整性校验和失效流程；现有无头回放没有可复用的持久缓存格式，当前项目级测量也无法将磁盘缓存收益与操作系统文件缓存收益分离。该项记录为跳过，没有新增运行时代码。

### 进程内存采样


使用 Windows 进程采样，每 250 ms 读取一次 `WorkingSet64` 和 `PrivateMemorySize64`。10 s `amairo` 回放的结果：

| 指标 | 峰值 |
| --- | ---: |
| Working Set | 1,685,209,088 bytes，约 1.57 GiB |
| Private Bytes | 1,716,887,552 bytes，约 1.60 GiB |
| 采样点 | 352 |
| 进程退出码 | 0 |

该采样反映进程工作集与私有字节，不能替代 Go heap live bytes、保留 descriptor bytes 或 GPU 显存统计。当前结果说明 `amairo` 的资源生命周期和峰值内存值得优先测量。

### 脚本编译准备缓存（跳过）

`Session.executeSource` 的 source+name+expression 编译缓存候选已在完整 `amairo` replay 中测量。10 s 回放从 61.061 s 变为 65.535 s，输出 SHA-256 保持一致；映射查找和缓存占用抵消了编译收益，代码已撤回。

## Go benchmark 结果

以下数值取三次运行中的中位数。不同 benchmark 的输入规模不同，结果用于同一 benchmark 的前后比较。

### TJS

| Benchmark | 中位数 | 分配 |
| --- | ---: | ---: |
| `VMRunPhases/compile-only` | 46.4 µs/op | 60,632 B/op，51 allocs/op |
| `VMRunPhases/first-run` | 659.7 µs/op | 1,017,129 B/op，3,019 allocs/op |
| `VMRunPhases/repeated-run` | 599.8 µs/op | 940,075 B/op，2,518 allocs/op |
| `VMMemberAccess/ordinary-hit` | 72.5 ns/op | 0 B/op，0 allocs/op |
| `VMMemberAccess/ordinary-miss` | 121.4 ns/op | 16 B/op，1 alloc/op |
| `VMMemberAccess/class-base-hit` | 370.1 ns/op | 64 B/op，3 allocs/op |
| `VMCallPaths/native/none` | 1.29 µs/op | 2,112 B/op，6 allocs/op |
| `VMCallPaths/script/fixed` | 7.18 µs/op | 9,737 B/op，25 allocs/op |
| `VMCallPaths/method/fixed` | 10.33 µs/op | 11,177 B/op，29 allocs/op |
| `VMPropertyAccess` | 12.85 µs/op | 16,043 B/op，51 allocs/op |
| `EqualUTF16/equal/32` | 9.34 ns/op | 0 B/op，0 allocs/op |

当前直接 UTF-16 equality 路径在 32-unit equal 输入上约为 9.34 ns/op；compare baseline 的中位数约为 10.39 ns/op。该结果属于 helper benchmark，尚未转换成完整 VM 或项目级加速结论。

`UTF16ToString` 的 ASCII short 输入保持 24 B/op、1 alloc/op。Windows 本次结果的时间中位数约 88.7 ns/op，stdlib 对照约 85.4 ns/op，说明分配行为稳定，时间结果仍受本机调度影响。

### CPU 合成与 transition

| Benchmark | 中位数 | 分配 |
| --- | ---: | ---: |
| `ComposeAlphaOverOpaqueDestination` | 6.33 ms/op | 4,612,101 B/op，2 allocs/op |
| `ComposeFullFrame` | 11.33 ms/op | 4,612,098 B/op，2 allocs/op |
| `ClearRGBA720p` | 416 µs/op | 3,686,414 B/op，1 alloc/op |
| `StretchCopyPixels/1280x720/nearest` | 1.02 ms/op | 10,240 B/op，1 alloc/op |
| `StretchCopyPixels/1280x720/linear` | 6.72 ms/op | 49,152 B/op，1 alloc/op |
| `BlendCrossfadeZeroOpaque/phase_64/generic` | 3.05 ms/op | 0 B/op，0 allocs/op |
| `BlendCrossfadeZeroOpaque/phase_64/specialized` | 2.51 ms/op | 0 B/op，0 allocs/op |
| `HashLayerRenderState` | 573 µs/op | 0 B/op，0 allocs/op |

CPU profile 使用 `BenchmarkComposeFullFrame`，5 s profile 的主要样本为：

- `(*drawContext).drawLayerTo`：87.83% flat，88.33% cumulative；
- `runtime.memclrNoHeapPointers`：4.17% flat。

当前 CPU 合成主要成本集中在 `drawLayerTo` 内层像素路径，符合完整 1280×720 合成的 benchmark 结果。

### GPU 原生图层与稳定 RenderPlan

`BenchmarkGPUComposeNativeLayers` 使用 1280×720 binder、8 个全画面原生图层和复用纹理。稳定行重复同一个 `StateHash`，动态行将 `StateHashStable` 设为 false。

| 场景 | 缓存改动前 | 当前实现 | 时间变化 | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| opaque/stable | 5.435 µs/op | 2.987 ns/op | -99.95% | 40 → 0 |
| alpha/stable | 5.303 µs/op | 3.327 ns/op | -99.94% | 40 → 0 |

稳定行显示重复 RenderPlan 时，GPU compositor 可以复用当前 target，跳过纹理访问、target clear 和图层绘制。动态计划继续使用原 `Compose` 路径，稳定缓存结果没有覆盖动态帧。

该 benchmark 测量无窗口 Ebitengine GPU `Compose` 稳定短路调用。窗口 Present、VSync、GPU queue 和显示刷新另行处理，稳定行的百分比不能当作完整 Update 或整帧显示加速。

可重复命令：

```sh
go test ./internal/host -run '^$' -bench '^BenchmarkGPUComposeNativeLayers$' -benchmem -benchtime=1s -count=3
```

缓存改动前结果来自同一 benchmark、同一机器和同一参数；基线运行时暂时移除 GPU target cache，测量完成后恢复当前实现。

### GPU 原生图层临时分配

`drawNode` 原生图层路径不使用 shader `uniforms` map。map 创建动作移动到 transition 和通用 shader 分支，原生 opaque/alpha 图层保持直接绘制。

| 场景 | 优化前 | 当前 | 时间中位数变化 | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| opaque/dynamic | 5.360 µs/op | 5.211 µs/op | -2.8% | 6157 → 5830 | 40 → 32 |
| alpha/dynamic | 5.417 µs/op | 5.496 µs/op | +1.5% | 6326 → 5954 | 40 → 32 |

同一 benchmark 中，原生图层每次操作减少 8 次分配，分配字节数下降约 5%。时间结果受 GPU 命令提交和本机调度影响，当前优化的稳定收益集中在分配数量。

可重复命令：

```sh
go test ./internal/host -run '^$' -bench '^BenchmarkGPUComposeNativeLayers/(opaque|alpha)/dynamic$' -benchmem -benchtime=1s -count=5
```

优化前后均使用 1280×720 binder、8 个全画面原生图层、复用纹理和同一 Windows amd64 环境。


### host 上传准备

| Benchmark | 中位数 | 分配 |
| --- | ---: | ---: |
| `UpdatePresentation720pOpaque` | 158.6 µs/op | 0 B/op，0 allocs/op |
| `UpdatePresentation720pSinglePixelChange` | 2.064 ms/op | 0 B/op，0 allocs/op |

Windows baseline above shows the former full-scan cost. The current opaque
dynamic path compares rows with `bytes.Equal`, then inspects only changed rows;
mixed-alpha frames still use the premultiplication scan.

### Darwin/arm64 follow-up

The latest focused measurements ran on Apple M1 Pro after the CPU, host, and
TJS fast paths were added:

| Benchmark | Before | Current | Change |
| --- | ---: | ---: | ---: |
| `ComposeFullFrame` | 8.195 ms/op | 4.780 ms/op | -41.7% |
| `UpdatePresentation720pSinglePixelChange` | 2.119 ms/op | 201.8 µs/op | -90.5% |
| `VMRunPhases/repeated-run` | 277.1 µs/op | 275.3 µs/op | -0.6% |

The compositor and host paths retained their existing allocation counts. The
TJS repeated-run allocation result remained 940,072 B/op and 2,518 allocs/op.
The host improvement applies to dynamic frames whose previous presentation
surface is opaque; mixed-alpha frames retain the existing premultiplication
path.

### CPU 静态帧上传短路

静态帧缓存现在向 `FrameSnapshot` 传递稳定的 render-state hash。host 在尺寸和 hash 同时一致时直接复用已上传图像，transition、video 和其他动态帧继续走原扫描路径。

| Benchmark | 优化前 | 当前实现 | 时间变化 | 分配 |
| --- | ---: | ---: | ---: | ---: |
| `UpdatePresentation720pStableRenderState` | 138.061 µs/op | 2.822 ns/op | -99.998% | 0 B/op，0 allocs/op |

该 benchmark 使用 1280×720 opaque 静态帧，前后均为同一帧数据。优化前后对照使用同一机器、同一参数；当前结果移除了 host 对 3.5 MiB RGBA 缓冲的重复扫描。

可重复命令：

```sh
go test ./internal/host -run '^$' -bench '^BenchmarkUpdatePresentation720pStableRenderState$' -benchmem -benchtime=500ms -count=3
```


### 资产与 XP3

| Benchmark | 中位数 | 分配 |
| --- | ---: | ---: |
| `StorageImageExtensionProbe/optimized` | 1.05 ms/op | 16,031 B/op，231 allocs/op |
| `StorageImageExtensionProbe/resolve_then_read` | 1.32 ms/op | 16,031 B/op，231 allocs/op |
| `RepeatedRawSegmentRead/cache_hit` | 82.8 µs/op | 524,471 B/op，7 allocs/op |
| `RepeatedRawSegmentRead/cache_disabled` | 207.3 µs/op | 1,049,635 B/op，8 allocs/op |

合成 XP3 输入中，缓存命中读取约为关闭缓存路径的 40%。该结果反映存储层 benchmark 行为，`amairo` 的完整启动仍包含大量 archive discovery、脚本执行和图片访问。

## 测量异常与范围

初次运行全部 assets benchmark 时有两项无法完成：

1. `BenchmarkCachedSegmentRead` 使用的 synthetic segment range 与空 archive 长度不一致，报告 `range 0x1+0x100000 exceeds 0x0`。
2. `BenchmarkLovelydayStorageExtensionProbes` 在 Windows 清理临时目录时无法删除仍被占用的 `data\image00.xp3`。

有效资产 benchmark 已单独重跑，结果记录在上表。上述两项未用于数值结论。

无头回放的墙钟时间包含：

- `amairo` 启动脚本和 archive 初始化；
- 每个 tick 的 runtime 工作；
- `Session.Frame` CPU 捕获；
- replay 输出对每帧 RGBA 执行 SHA-256。

因此 1 s 和 10 s 的墙钟值适合当作端到端无头参考，不适合直接当作单帧 CPU 合成耗时。CPU 合成、TJS 和资产路径使用独立 benchmark 进行细分。

## 可重复基线

性能改动后建议固定执行：

```sh
go test ./internal/tjs -run '^$' -bench . -benchmem -benchtime=500ms -count=3
go test ./internal/runtime -run '^$' -bench . -benchmem -benchtime=500ms -count=3
go test ./internal/host -run '^$' -bench . -benchmem -benchtime=500ms -count=3
go test ./internal/assets -run '^$' -bench '^BenchmarkStorageImageExtensionProbe$' -benchmem -benchtime=500ms -count=3
go test ./internal/assets -run '^$' -bench '^BenchmarkRepeatedRawSegmentRead$' -benchmem -benchtime=500ms -count=3

printf '[{"at_ms":10000,"action":"checkpoint","name":"end","inspect":[]}]' | temp/krkrgo-tool-perf.exe replay \
  --project testdata/amairo \
  --input - \
  --output temp/perf-amairo-10s.json \
  --step-ms 16 \
  --budget 100000000
```

每次记录：

- OS、架构、CPU、Go 版本；
- benchmark 中位数、B/op、allocs/op；
- `amairo` 回放墙钟耗时；
- frame 数量、最后虚拟时间、frame SHA-256；
- Working Set 与 Private Bytes 峰值；
- replay 错误和 checkpoint 状态。

## 十项优化验收状态

| 编号 | 方向 | 状态 | 提交 |
| --- | --- | --- | --- |
| 1 | TJS Legacy 紧凑执行形式 | 跳过：`amairo` 10 s 为 63.111 s，高于 61.061 s | `cf0227a` |
| 2 | GPU RenderItem 与批量绘制 | 跳过：当前 backend 没有稳定批量收益 | `459c0c5` |
| 3 | 持久化 TJS 准备缓存 | 跳过跨启动部分；进程内缓存已在 `2276a6d` 获得收益 | `3decae2` |
| 4 | CPU dirty-region / tile cache | 跳过：当前 layer 状态没有统一区域失效信息 | `2cc21aa` |
| 5 | TJS 属性与调用 inline cache | 跳过：缺少稳定 receiver-shape / call-target 项目测量 | `a5fe83d` |
| 6 | VM frame / scope / register 复用 | 跳过：寄存器候选使 10 s 回放变慢 | `fc4da3a` |
| 7 | 脚本编译准备缓存 | 跳过：10 s 回放从 61.061 s 变为 65.535 s | `6fd4240` |
| 8 | 异步资源读取、解码与预取 | 跳过：当前 `amairo` 没有独立资源切换测量场景 | `cca57c8` |
| 9 | FFmpeg 硬件解码与 GPU 帧链路 | 跳过：当前 sidecar 没有硬件帧路径 | `e8badc3` |
| 10 | GPU transition 全量迁移 | 跳过重复改动：当前 crossfade/universal 已使用 Kage shader | `e445027` |

运行时代码提交条件：同场景测量结果必须显示正向收益。其余项目均保留测量结论或能力限制记录。


## 新增高收益优化

### Replay 借用帧摘要

`internal/host/replay.go` 原先通过 `Session.Frame()` 获取拥有副本，再立即计算 RGBA SHA-256。回放摘要不会保存或修改帧内容，因此改用新的 `Session.CaptureFrameBorrowed()`；公共 `CaptureFrame()` 仍保留拥有副本的接口合同。

同机、同输入、`amairo` 1 s 无头回放：

- 优化前：`19.33 s`
- 优化后：`15.75 s`
- 墙钟耗时降低：`18.5%`
- 优化后重复运行：`17.30 s`
- 两次输出 JSON SHA-256：`6cf00eea902de349bf781b2e442cde87e5d6f394956d5b4afec58607ca131969`

该改动直接减少每帧 RGBA 拷贝，摘要读取完成后借用帧即可继续由 Services 复用。`TestSessionBorrowedFrameMatchesOwnedFrame` 覆盖了借用帧与拥有帧的像素结果一致性。

### onPaint 待处理队列

帧捕获原先每次遍历全部 `Services.Layers`，再筛选 `callOnPaint`。Layer.update 和 `callOnPaint` setter 现在维护待处理 ID 队列，捕获阶段按 ID 排序后直接分发，未触发 onPaint 的帧不再遍历整个 layer map。

同一优化组合的 `amairo` 1 s 无头回放：

- 借用帧后：`15.75 s`
- 加入 onPaint 队列后：`13.06 s`
- 相对原始基线 `19.33 s`：总耗时降低 **32.5%**
- 输出 JSON SHA-256 保持 `6cf00eea902de349bf781b2e442cde87e5d6f394956d5b4afec58607ca131969`


### Follow-up production paths

The current implementation also includes five focused paths selected from the
measured candidates:

- `internal/runtime/layer_draw.go` copies source bytes directly for
  alpha-over-transparent destinations.
- `internal/host/ebiten.go` compares opaque dynamic frames row by row and
  uploads only the changed rectangle when the frame remains opaque.
- `internal/tjs/value.go` and `internal/tjs/vm.go` use a direct integer
  operation path for VM binary operators.
- `internal/tjs/gc.go` retains VM-local collector workspace for repeated
  collections.
- `internal/runtime/session.go` transfers the pending `onPaint` queue into the
  capture pass without cloning its ID slice.

The focused tests passed for compositor alpha behavior, presentation fallback,
TJS binary operators, garbage collection, and onPaint queue dispatch.

### Current Darwin/arm64 implementation follow-up

The latest package-level checks also cover the following paths:

- `BenchmarkVMRunPhases/repeated-run`: about 107.7 µs/op, 11,440 B/op, and
  907 allocs/op after direct register operands and pointer-based instruction
  fetch.
- `BenchmarkVMStaticMemberAccess`: about 2.31 µs/op, 2,289 B/op, and 12
  allocs/op for static keys; the dynamic-key form measured about 2.94 µs/op,
  2,315 B/op, and 15 allocs/op.
- `BenchmarkVMPropertyAccess`: about 4.47 µs/op, 4,400 B/op, and 31 allocs/op
  after direct member register access.
- `BenchmarkUpdatePresentation720pTransparentSinglePixelChange`: about
  5.4 ms/op with zero reported allocations. The path uses one premultiplication
  scan for initial and subsequent mixed-alpha frames.
- `BenchmarkImageDecodeCacheLargeLookup`: about 91 ns/op, 8 B/op, and one
  allocation with 4,096 indexed LRU entries.
- `BenchmarkCachedSegmentLargeLookup`: about 17.8 ns/op with zero allocations
  with 4,096 indexed LRU entries.
- `BenchmarkBlendUniversal1280x720`: about 3.03 ms/op for the optimized
  kernel versus 5.05 ms/op for the former channel loop, with zero
  allocations.
- `BenchmarkCaptureRenderPlanUniversalTransition`: about 0.51 µs/op with
  cached internal transition hashes versus 1.29 ms/op with full buffer
  hashing on the 1280×720 fixture.
- GPU composition now clears the front target once per compose; the back
  target is fully overwritten by the existing copy before shader use.
- `BenchmarkComposeOpaqueFullCanvas`: about 0.629 ms/op when the conservative
  full-canvas opaque-layer check skips the background RGBA fill, versus about
  0.712 ms/op with the equivalent fill; both paths report two allocations.

These are isolated Darwin/arm64 package benchmarks. They do not establish a
whole-project replay speedup.

## 当前判断

1. `amairo` 无头启动和 10 s 回放的墙钟成本很高，启动资源初始化与 CPU frame 处理需要继续细分。
2. `amairo` 10 s 回放的进程峰值工作集约 1.57 GiB，资源所有权、archive 缓存和 decoded image 生命周期是高优先级测量方向。
3. `drawLayerTo` remains the main CPU composition hotspot; the focused full-frame fixture moved from 8.195 ms/op to 4.780 ms/op after the transparent-destination fast path.
4. Opaque dynamic host presentation moved from 2.119 ms/op to 201.8 µs/op for a single-pixel change, with zero allocations. Mixed-alpha frames retain the premultiplication scan.
5. The focused TJS `VMRunPhases/repeated-run` fixture moved from 277.1 µs/op to 275.3 µs/op. It remains at 940,072 B/op and 2,518 allocs/op.
6. XP3 cache hit 明显低于关闭缓存路径，存储缓存对重复读取有效。
7. 无头结果可以作为 CPU、分配、资源和内存方向的主基线；窗口 Present、GPU queue、VSync 和显示延迟需要单独测量。

### 2× 渲染与 host 上传优化

本轮针对 2× 缓冲区增加的像素带宽，加入了以下路径：

- `internal/runtime/parallel_rows.go` 按行带拆分 CPU 合成，调用方保证每个工作协程只读写自己的行；`drawLayerTo`、alpha 快速路径和 `copyRect` 行复制都使用该拆分。
- `internal/simd` 提供架构专用实现：arm64 使用 NEON，amd64 使用 SSE2；其他架构使用纯 Go 回退。当前 SIMD 覆盖 alpha 全不透明行扫描、alpha-over 不透明目标行混合和 2× 行复制。`drawOpaqueAlphaLayer` 与 `drawOpaqueAlphaLayerNoProvince` 在目标行 alpha 全为 255 时调用 SIMD 混合，混合公式与标量路径使用同一整数舍入；全不透明源行走整行复制，province 平面单独更新；`scaleRGBA` 在 2× 时调用 SIMD 行复制，再复制到其余设备行；`fillLayerNeutral` 使用 32 位像素写入。
- `internal/runtime/layer.go` 增加 dirty rect。`drawText`、`fillRect`/`colorRect`、`copyRect` 记录设备像素矩形，`CaptureRenderPlan` 将矩形带入 `RenderNode` 后清空。
- `internal/host/gpu_compositor.go` 在纹理已上传且 dirty rect 小于整图时，只预乘并 `WritePixels` 变化子矩形；不透明层保持整层或局部上传语义。
- GPU 纹理按尺寸进入有上限的池，渲染计划中未使用的图层纹理在动态合成结束时回收到池；不可见图层因此在隐藏后释放纹理。
- 全屏 crossfade/universal transition 跳过 `copyFrontToBack`，由 shader 一次覆盖 back target；非全屏 transition 仍复制以保留目标区域。
- GPU-native crossfade/universal 的 CPU `transitionPixels` 改为按需分配，shader 路径不再保存整帧 CPU 相位缓冲。

Darwin/arm64、Apple M1 Pro 的包级结果：

| Benchmark | 当前 |
| --- | ---: |
| `BenchmarkComposeFullFrame` | 0.88–0.90 ms/op |
| `BenchmarkComposeFullFrameScale2` | 2.91–2.96 ms/op |
| `BenchmarkGPUComposeNativeLayers/opaque/dynamic` | 3.66 µs/op，28 allocs/op |
| `BenchmarkGPUComposeNativeLayers/alpha/dynamic` | 3.51 µs/op，28 allocs/op |
| `BenchmarkAllAlphaOpaque` | 138–146 ns/op，35–37 GB/s |
| `BenchmarkAllAlphaOpaqueScalar` | 414–437 ns/op，12 GB/s |
| `BenchmarkScale2xRow` | 135–139 ns/op，37–38 GB/s |
| `BenchmarkScale2xRowScalar` | 1.83–1.87 µs/op，2.8 GB/s |

`BenchmarkComposeFullFrame` 的此前同机结果约为 3.0 ms/op；行带并行和字宽 alpha 检查把 1× 合成降到约 0.9 ms/op。2× 合成约为 1× 的 3.3 倍，低于纯像素量的 4 倍。GPU 动态合成 benchmark 不包含纹理上传、GPU 执行和 Present，上传收益需要用实窗 `KRKRGO_RENDER_STATS=1` 的 `upload_avg` 验证。

SIMD 行扫描约比标量行扫描快 3 倍，alpha-over 不透明目标行混合约快 3 倍，2× 行复制约快 13 倍。1280×720 全屏半透明图层带 province 的合成从约 1.4 ms/op 降到约 0.68 ms/op。amd64 的 SSE2 路径已用 Rosetta 运行 `internal/simd`、`internal/runtime` 和 `internal/host` 测试；linux/windows amd64、linux/windows arm64、darwin amd64 均已交叉编译通过。非 SIMD 架构的纯 Go 回退也已通过 linux/386 编译。

这些是孤立包级 benchmark；整项目回放和交互帧时间仍需单独测量。


### 帧超分后处理

`bilinear`、`bicubic`、`anime4k` 三种帧后处理器的实现、缓存、限制和验证命令集中在 [Frame Upscaling](upscaling.md)。
