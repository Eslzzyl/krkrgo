# 标题画面过渡问题交接

## 当前状态

- 任务已中止。
- 当前工作树有 5 个未提交的生产/测试文件：
  - `internal/host/gpu_compositor.go`
  - `internal/runtime/layer.go`
  - `internal/runtime/layer_transition_native.go`
  - `internal/runtime/render_plan.go`
  - `internal/runtime/render_plan_test.go`
- 上一个已提交修复为 `925097b Fix cursor-triggered deferred GPU image materialization`，内容对应菜单区域随光标移动出现黑色背景的问题。
- 本次标题画面问题没有提交。
- 本次排查没有使用 CUA，也没有启动可见窗口。无窗口诊断进程均已结束。

## 已完成改动

### GPU 过渡端点

`internal/runtime/layer_transition_native.go` 增加了 GPU 过渡端点准备逻辑：

- 对 `crossfade` 和 `universal`、`withchildren` 过渡，按完整图层树生成 destination/source 图像。
- 生成前调用 `materializeLayerTree`，涵盖延迟 GPU 图像操作。
- 保存端点尺寸、哈希值、Alpha 分类结果。
- 取消过渡与完成过渡时清理这些缓存。

`internal/runtime/render_plan.go` 在 GPU 计划捕获阶段准备端点，并将完整子树图像写入 `RenderTransition`。

### GPU 绘制

`internal/host/gpu_compositor.go`：

- `withchildren` 过渡使用过渡端点尺寸与原点计算裁剪区域。
- 过渡节点已经包含完整子树图像时，跳过该节点的递归子节点绘制。

### 测试

`internal/runtime/render_plan_test.go` 增加了
`TestCaptureRenderPlanTransitionWithChildrenUsesSubtreeImages`：

- destination 子树使用红色像素。
- source 子树通过延迟 GPU copy 产生蓝色像素。
- 断言计划中的两个端点都包含完整子树结果。

## 已验证结果

此前运行通过：

- `go test ./internal/runtime ./internal/host`
- `go test ./...`
- `git diff --check`

无窗口宿主诊断使用了 Ebitengine VM host，并在每帧加入 16ms 时钟推进：

- 默认图形路径可以从启动素材过渡到标题画面。
- `EBITENGINE_GRAPHICS_LIBRARY=directx` 路径也能输出标题过渡。
- 采样帧显示标题过渡阶段存在，最终标题画面正常显示。
- 无时钟推进时画面停留在启动素材，说明诊断驱动需要真实时间推进。

运行时计划诊断显示：

- 过渡节点为 `withchildren=true`。
- 过渡方法为 `crossfade`。
- 约 `4480ms` 时 phase 为 `195`，destination 首像素为 `[249 251 252 255]`，source 首像素为 `[255 255 255 255]`，Alpha 分类为 opaque。
- 约 `4608ms` 时 phase 为 `255`，随后过渡完成并显示交换后的标题图层树。

## 最新用户反馈

用户反馈当前画面又变成白屏。此前反馈曾出现黑屏，随后反馈仍为黑屏。

## 尚未完成的判断

当前计划端点显示为“标题图像 → 白色 source → 交换后标题图像”的流程：

- 运行时当前过渡图层包含标题子树。
- 隐藏 source 图层包含白色启动图像。
- phase 从 0 增加到 255 后执行 exchange。

这一流程与用户期望的“白色启动画面进入标题画面”方向存在差异，需继续核对 exchange 过渡的端点方向与 phase 语义。

参考实现 `reference/krkrz/visual/LayerIntf.cpp` 使用：

- `Src1Bmp = Complete()`
- `Src2Bmp = TransSrc->Complete()`
- `exchange` 完成后交换图层树

项目现有生命周期测试覆盖中点颜色与交换后的图层关系，尚未覆盖“exchange 过渡方向”的像素序列。

## 临时文件清理清单

最终交付前需要删除本次诊断创建的文件：

- `temp/title_nodes_probe.go`
- `temp/headless_guest/main.go`
- `temp/headless_driver/main.go`
- `temp/title-headless_*.png`
- `temp/title-headless-late_*.png`
- `temp/title-headless-sleep_*.png`
- `temp/title-headless-sleep-long_*.png`
- `temp/title-headless-stages_*.png`
- `temp/title-headless-entry_*.png`
- `temp/title-headless-transition_*.png`
- `temp/title-headless-directx_*.png`

清理后再次确认工作树只保留 5 个目标改动文件，再执行相关测试。