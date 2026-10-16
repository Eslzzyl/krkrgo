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

### 进程内存采样

使用 Windows 进程采样，每 250 ms 读取一次 `WorkingSet64` 和 `PrivateMemorySize64`。10 s `amairo` 回放的结果：

| 指标 | 峰值 |
| --- | ---: |
| Working Set | 1,685,209,088 bytes，约 1.57 GiB |
| Private Bytes | 1,716,887,552 bytes，约 1.60 GiB |
| 采样点 | 352 |
| 进程退出码 | 0 |

该采样反映进程工作集与私有字节，不能替代 Go heap live bytes、保留 descriptor bytes 或 GPU 显存统计。当前结果说明 `amairo` 的资源生命周期和峰值内存值得优先测量。

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


### host 上传准备

| Benchmark | 中位数 | 分配 |
| --- | ---: | ---: |
| `UpdatePresentation720pOpaque` | 158.6 µs/op | 0 B/op，0 allocs/op |
| `UpdatePresentation720pSinglePixelChange` | 2.064 ms/op | 0 B/op，0 allocs/op |

单像素变化仍触发约 2 ms 的扫描成本。changed-bounds 可以减少后续写入范围，当前 benchmark 显示扫描本身仍是重要固定成本。

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

## 当前判断

1. `amairo` 无头启动和 10 s 回放的墙钟成本很高，启动资源初始化与 CPU frame 处理需要继续细分。
2. `amairo` 10 s 回放的进程峰值工作集约 1.57 GiB，资源所有权、archive 缓存和 decoded image 生命周期是高优先级测量方向。
3. CPU 合成的主要热点是 `drawLayerTo`；完整 1280×720 合成约 11.33 ms/op。
4. host 上传准备在整帧不变时约 0.159 ms/op，单像素变化约 2.064 ms/op，帧扫描仍有明显固定成本。
5. TJS repeated-run 约 0.600 ms/op，且每次约 940 KB、2,518 allocs；帧、调用和临时容器仍有较大优化空间。
6. XP3 cache hit 明显低于关闭缓存路径，存储缓存对重复读取有效。
7. 无头结果可以作为 CPU、分配、资源和内存方向的主基线；窗口 Present、GPU queue、VSync 和显示延迟需要单独测量。
