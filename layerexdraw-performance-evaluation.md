# layerExDraw 性能与渲染路径技术评估

## 评估目标

本文件面向代码分析模型，目标是评估当前 `layerExDraw` 方案 A 实现的功能覆盖、数据流和性能影响。实现路径已经固定为：CPU 光栅化写入 `Layer.RGBA`，随后复用现有 RenderPlan 与 GPUCompositor 上传纹理。

评估重点：

1. `LayerExDraw` 状态与 `Layer.RGBA` 的生命周期是否一致。
2. 图元、文字、图片、变换、记录回放是否经过统一的脏矩形更新。
3. GPU 默认显示模式下，CPU 光栅化和 CPU→GPU 像素上传的成本如何随 Layer 尺寸、调用次数、覆盖面积变化。
4. 当前实现与 `reference/krkr2/cpp/plugins/layerex_draw` API 的行为差异。

当前代码表明，GPU 默认模式负责交互式最终合成；Layer 的脚本图像状态仍以 CPU RGBA 缓冲区为主要表示。这个区别决定了 CPU 绘图方案可以进入当前 GPU 流程，但动态大画面会产生 CPU 绘图和 CPU→GPU 像素传输成本。


## 2. 结论先行

### 2.1 已确认的架构结论

- `NewGame` 默认选择 `rendererGPU`。
- GPU 模式捕获 `runtime.RenderPlan`，由 `GPUCompositor` 合成。
- 普通 Layer 节点通过 `RenderNode.RGBA` 提供图像数据。
- `GPUCompositor` 为普通 Layer RGBA 建立并缓存 Ebitengine 纹理。
- Layer 图像发生变化后，GPU 合成器根据脏矩形提交整图或子矩形上传。
- `MotionPlayer` 已有专用延迟 GPU 表示 `RenderMotion`；普通 `layerExDraw` 尚无同类命令表示。
- Krkr2 的 Windows `layerExDraw` 参考实现直接在 Layer 提供的像素缓冲区上创建 GDI+ Bitmap，绘制完成后调用 Layer 的 `update`。

### 2.2 性能判断

- 静态绘制或小区域更新：CPU 光栅化后上传的成本通常可控。
- 每帧更新大面积 Layer：CPU 光栅化、透明度转换和 GPU 上传会叠加，成本可能明显。
- 1920×1080 的整张 Layer 每帧变化，原始 RGBA 上传量约为 7.91 MiB/frame，60 FPS 约为 474.6 MiB/s，尚未计入 CPU 光栅化、预乘转换、驱动处理和 GPU 合成。
- GPU 默认模式本身无法消除这项成本，因为普通 Layer 的数据源仍是 CPU RGBA。
- GPU 原生 `layerExDraw` 需要扩展后端数据模型，不属于现有 `GPUCompositor` 的局部改动。

### 2.3 方案 A 已实施状态

`layerexdraw.dll` 已接入 runtime 插件链接流程。实现位于 `internal/runtime/layer_ex_draw_native.go`，核心状态存储在 `Services.LayerExDraws` 与 `Services.LayerExObjects`。

当前 API 覆盖：

- `LayerExDraw` 属性：`updateWhenDraw`、`smoothingMode`、`textRenderingHint`、`record`。
- Layer 变换：ViewTransform 与 Transform 的设置、重置、旋转、缩放、平移。
- 图元：弧线、扇形、Bezier、连续 Bezier、Cardinal 曲线、椭圆、直线、多线段、多边形、矩形、矩形数组、Path。
- 文字：`drawString`、`drawPathString`、`measureString`、`measureStringInternal` 及对应别名。
- 图片：整图、源矩形、缩放、仿射变换、Layer/GdiPlus.Image/storage name 输入、RotateFlip、Clone。
- 记录：记录缓冲区、记录图像读取、重绘、保存、读取。
- `GdiPlus` 对象：`PointF`、`RectF`、`Matrix`、`Image`、`Font`、`Appearance`、`Path`，以及字体加载和常量注册。
- 画刷和画笔：纯色、线性渐变、纹理、Hatch、PathGradient、线宽、虚线、偏移、抗锯齿。

统一数据流：

```text
LayerExDraw native call
    -> LayerExDraw state
    -> layerExTargetsLocked
    -> CPU rasterizer / text renderer / image sampler
    -> Layer.RGBA
    -> markImageDirtyRect
    -> RenderPlan.ImageDirty
    -> GPUCompositor.texture
```

实现使用 Layer 现有非预乘 RGBA 缓冲区。每次绘制通过 `layerExMarkLocked` 提交计算后的区域；`updateWhenDraw` 为真时追加 Layer paint 回调。Layer 销毁路径会清理对应的 LayerExDraw 状态。

当前性能结论：局部图元、文字、局部图片操作复用脏矩形上传；大面积逐帧绘制仍然承担 CPU 光栅化、透明度预乘和纹理上传成本。当前实现没有创建新的 GPU 图元命令格式。

## 3. 代码事实：默认 GPU 渲染路径

### 3.1 `NewGame` 的默认选择

来源：`internal/host/ebiten.go:297-305`

```go
func NewGame(session *runtime.Session) *Game {
	return newGameWithRenderer(session, rendererGPU)
}

func NewCPUGame(session *runtime.Session) *Game {
	return newGameWithRenderer(session, rendererCPU)
}
```

因此，交互式 `NewGame` 使用 GPU 模式；CPU 模式由显式的 `NewCPUGame` 选择。

### 3.2 `Game.Update` 的 GPU 分支

来源：`internal/host/ebiten.go:538-648`

关键流程：

```go
if g.rendererMode == rendererGPU {
	frame, plan, err = g.session.TickForGPUPresentationWithAudioDelta(...)
}

if g.rendererMode == rendererGPU {
	g.gpuImage, err = g.composeGPUPlan(plan)
}
```

`composeGPUPlan` 位于 `internal/host/ebiten.go:688-719`：

```go
if plan.StateHashStable && plan.StateHashValid {
	base, err = g.gpu.composeStable(plan)
} else {
	g.gpu.invalidateStateCache()
	base, err = g.gpu.Compose(plan)
}
```

GPU 模式不会把完整 `FrameSnapshot.RGBA` 交给 CPU 版 `Compositor` 再上传。它直接使用运行时捕获的 `RenderPlan`，将 Layer 节点交给 GPU 合成器。

### 3.3 GPU 捕获标志

来源：`internal/runtime/session.go:1506-1515`

```go
func (s *Session) tickForGPUPresentation(timeMS uint64, audioMilliseconds *uint64) (RenderPlan, error) {
	s.gpuCapture = true
	s.Services.gpuCapture = true
	defer func() {
		s.gpuCapture = false
		s.Services.gpuCapture = false
	}()
	return ...
}
```

这个标志影响 `CaptureRenderPlan` 对图像内容的处理方式。

## 4. 代码事实：Layer 的 CPU 图像状态

### 4.1 `Layer.RGBA`

来源：`internal/runtime/layer.go:168-186`

```go
type Layer struct {
	ID               LayerID
	Bounds           Rect
	Opacity          int
	Visible          bool
	Type             LayerType
	RGBA             []byte
	Province         []byte
	ImageWidth       int32
	ImageHeight      int32
	...
}
```

`Layer.RGBA` 是紧密排列的非预乘 RGBA，长度为：

```text
ImageWidth * ImageHeight * 4
```

脚本层面的 `fillRect`、`copyRect`、`stretchCopy`、`drawText` 等操作最终都围绕 CPU 图像数据工作。

### 4.2 `SetRGBA` 的复制语义

来源：`internal/runtime/layer.go:621-653`

```go
func (l *Layer) SetRGBA(width, height int32, rgba []byte) error {
	return l.setRGBA(width, height, rgba, false)
}

func (l *Layer) SetRGBAOwned(width, height int32, rgba []byte) error {
	return l.setRGBA(width, height, rgba, true)
}
```

```go
if owned {
	l.RGBA = rgba
} else {
	l.RGBA = append([]byte(nil), rgba...)
}
l.ImageWidth, l.ImageHeight = width, height
l.markImageDirty()
```

实现 `layerExDraw` 时，若绘制器直接持有 `Layer.RGBA`，可减少每次绘制产生的整图临时复制。若绘制器先生成独立 `[]byte` 再调用 `SetRGBA`，还会增加一次 CPU 内存复制。

### 4.3 脏矩形传播

来源：`internal/runtime/layer.go:252-292`

```go
func (l *Layer) markImageDirtyRect(rect Rect) {
	if l == nil {
		return
	}
	l.imageRevision++
	l.markRenderDirty()
	...
	if l.dirtyRectValid {
		l.dirtyRect = unionRect(l.dirtyRect, rect)
	} else {
		l.dirtyRect = rect
		l.dirtyRectValid = true
	}
}
```

多个绘制操作会将矩形合并到一个 Layer 脏矩形。若一个帧内的绘制区域分散在画布两端，合并结果可能接近整张 Layer，上传面积随之扩大。

### 4.4 `SetRGBAOwned` 与 `markImageDirty`

`SetRGBAOwned` 会直接接管调用者提供的数组，但随后调用 `markImageDirty()`，该函数使用完整设备尺寸记录脏矩形：

```go
func (l *Layer) markImageDirty() {
	l.markImageDirtyRect(Rect{Width: l.deviceWidth(), Height: l.deviceHeight()})
}
```

因此，`layerExDraw` 若采用逐个小图形直接写入现有 `Layer.RGBA`，应调用精确的 `markImageDirtyRect` 语义；若每次调用 `SetRGBAOwned`，默认会把整张 Layer 标记为变化。

## 5. 代码事实：RenderPlan 如何携带普通 Layer 图像

### 5.1 `RenderNode.RGBA`

来源：`internal/runtime/render_plan.go:18-44`

```go
type RenderNode struct {
	ID          LayerID
	Bounds      Rect
	Opacity     uint8
	Visible     bool
	Type        LayerType
	ImageWidth  int32
	ImageHeight int32
	RGBA        []byte
	ImageHash   uint64
	ImageDirty  Rect
	ImageDirtyValid bool
	...
}
```

来源：`internal/runtime/render_plan.go:318-342`

```go
n.RGBA = l.RGBA
```

这里是借用关系，`RenderPlan` 不复制 Layer 的像素数组。宿主在同一同步调用链中消费该计划。

### 5.2 GPU 捕获时使用 revision，而非重新扫描整张 RGBA

来源：`internal/runtime/render_plan.go:382-390`

```go
if len(l.transitionPixels) == 0 {
	if s.gpuCapture {
		hashes.rgba = renderPlanImageRevision(l)
	} else {
		hashes.rgba = hashBytes(n.RGBA)
	}
	n.ImageHash = hashes.rgba
}
```

GPU 捕获模式使用 Layer 的图像 revision。CPU 捕获模式会对 `n.RGBA` 做内容哈希。这个设计降低了 GPU 计划捕获阶段的整图扫描成本，但它没有消除后续 GPU 纹理更新的像素传输。

### 5.3 脏矩形写入 RenderPlan

来源：`internal/runtime/render_plan.go:479-484`

```go
dirty, dirtyValid := l.takeImageDirtyRect()
if p.Nodes[i].Transition == nil &&
	!p.Nodes[i].GPUStretch &&
	p.Nodes[i].Motion == nil {
	p.Nodes[i].ImageDirty = dirty
	p.Nodes[i].ImageDirtyValid = dirtyValid
}
```

普通 Layer RGBA 节点会携带 `ImageDirty`。`layerExDraw` 若正确维护脏矩形，可以让 GPU 上传使用局部区域。

## 6. 代码事实：GPU 纹理上传

### 6.1 纹理缓存

来源：`internal/host/gpu_compositor.go:73-115`

```go
type GPUCompositor struct {
	textures            map[gpuTextureKey]*gpuTexture
	motionTargets       map[runtime.LayerID]*ebiten.Image
	motionAtlases       map[runtime.LayerID]*gpuMotionAtlas
	motionStreams       map[runtime.LayerID]*gpuMotionStream
	uploadScratch       []byte
	motionPaddedScratch []byte
	...
}
```

普通 Layer 纹理使用 `textures` 缓存；Motion 专用的目标、图集和流式纹理使用其他字段。

### 6.2 普通 Layer 的上传决策

来源：`internal/host/gpu_compositor.go:413-443`

```go
func (c *GPUCompositor) texture(
	key gpuTextureKey,
	width, height int32,
	src []byte,
	hash uint64,
	opaque bool,
	dirty runtime.Rect,
	dirtyValid bool,
) (*ebiten.Image, error) {
	...
	if t.hash != hash || t.opaque != opaque {
		partial := dirtyValid &&
			t.uploaded &&
			t.opaque == opaque &&
			dirty.X >= 0 &&
			dirty.Y >= 0 &&
			dirty.Right() <= width &&
			dirty.Bottom() <= height &&
			!dirty.Empty() &&
			int64(dirty.Width)*int64(dirty.Height) <
				int64(width)*int64(height)
		if partial {
			upload := c.scratch(int(dirty.Width) * int(dirty.Height) * 4)
			premultipliedRegion(src, width, dirty, upload, opaque)
			t.image.SubImage(...).WritePixels(upload)
		} else {
			upload := c.premultipliedUpload(src, opaque)
			t.image.WritePixels(upload)
		}
		t.hash = hash
		t.uploaded = true
	}
	return t.image, nil
}
```

直接含义：

- Layer 内容未变化：复用已有纹理。
- 脏矩形小于整图：提交子矩形。
- 脏矩形覆盖整图、脏矩形无效、尺寸变化或纹理初次创建：提交整图。

### 6.3 预乘转换

来源：`internal/host/gpu_compositor.go:359-403`

```go
func premultipliedPixels(src []byte, dst []byte, opaque bool) {
	if simd.AllAlphaOpaque(src) {
		copy(dst, src)
		return
	}
	for i := 0; i+3 < len(src); i += 4 {
		a := uint16(src[i+3])
		if opaque {
			a = 255
		}
		dst[i+3] = byte(a)
		dst[i] = byte((uint16(src[i])*a + 127) / 255)
		...
	}
}
```

`Layer.RGBA` 使用非预乘 RGBA；Ebitengine 纹理路径需要预乘数据。透明图像上传会增加一次 CPU 通道遍历，并使用 `uploadScratch` 保存转换结果。

## 7. 代码事实：GPU 合成器当前支持的延迟表示

### 7.1 `RenderMotion` 是现有 GPU 专用路径

来源：`internal/runtime/render_plan.go:46-125`

```go
type RenderMotion struct {
	Filter int64
	Scale  int32
	AtlasKey string
	Width, Height int
	Draws []RenderMotionDraw
	Items []RenderMotionItem
}
```

来源：`internal/runtime/render_plan.go:102-125`

```go
type RenderMotionDraw struct {
	SourceKey string
	RGBA      []byte
	Width, Height int32
	A, B, C, D float64
	TX, TY float64
	Opacity uint8
	Opaque bool
	Premultiplied bool
	Blend MotionBlendMode
	Vertices []MotionVertex
	Indices []uint16
	Baked bool
}
```

### 7.2 普通 Layer 与 Motion 的差异

`CaptureRenderPlan` 在 GPU 捕获模式下遇到 `l.gpuMotion` 时，会将 Motion 描述放入 `RenderNode.Motion`，同时清空 `RenderNode.RGBA`：

来源：`internal/runtime/render_plan.go:371-380`

```go
if l.gpuMotion != nil && len(l.transitionPixels) == 0 {
	if s.gpuCapture {
		n.Motion = l.gpuMotion
		n.RGBA = nil
	} else {
		if err := l.materializeGPUMotion(); err != nil {
			return -1, err
		}
		n.RGBA = l.RGBA
	}
}
```

当前没有 `RenderLayerExDraw`、`RenderVectorCommand` 或同等通用类型。若 `layerExDraw` 采用 GPU 原生路径，需要新增类似 `RenderMotion` 的后端无关描述，或者直接在 `GPUCompositor` 内部建立与 Layer ID 关联的 GPU 绘制状态。

## 8. 参考 C++ 的实际绘制模型

### 8.1 `layerExBase` 获取 Layer 写缓冲区

来源：`reference/krkr2/cpp/plugins/layerexBase.hpp:71-132`

```cpp
layerExBase(DispatchT obj) :
    _obj(obj),
    _pWidth(obj, TJS_W("imageWidth")),
    _pHeight(obj, TJS_W("imageHeight")),
    _pBuffer(obj, TJS_W("mainImageBufferForWrite")),
    _pPitch(obj, TJS_W("mainImageBufferPitch")),
    _pUpdate(obj, TJS_W("update")),
    ...
{}
```

```cpp
virtual void reset() {
    _width = (GeometryT)_pWidth;
    _height = (GeometryT)_pHeight;
    _buffer = (BufferT)(ObjectCache::IntegerT)_pBuffer;
    _pitch = (PitchT)_pPitch;
    ...
}
```

参考插件的基础模型是：

1. 读取 Layer 的尺寸、pitch 和可写图像缓冲区。
2. 在该缓冲区上执行绘制。
3. 通过 `_pUpdate` 通知 Layer 更新。

### 8.2 Windows GDI+ 实现直接绑定共享缓冲区

来源：`reference/krkr2/cpp/plugins/layerex_draw/windows/LayerExDraw.cpp:945-977`

```cpp
void LayerExDraw::reset() {
    layerExBase::reset();
    if(!(graphics && width == _width && height == _height &&
         pitch == _pitch && buffer == _buffer)) {
        delete graphics;
        delete bitmap;
        width = _width;
        height = _height;
        pitch = _pitch;
        buffer = _buffer;
        bitmap = new Bitmap(width, height, pitch,
                            PixelFormat32bppARGB,
                            (unsigned char *)buffer);
        graphics = new Graphics(bitmap);
        graphics->SetCompositingMode(CompositingModeSourceOver);
        graphics->SetTransform(&calcTransform);
    }
}
```

这个实现没有为每次绘制建立独立画布。GDI+ `Bitmap` 直接包住 Layer 写缓冲区，绘制结果立即进入 Layer 图像。

### 8.3 Windows 更新回调提供局部矩形

来源：`reference/krkr2/cpp/plugins/layerex_draw/windows/LayerExDraw.cpp:908-915`

```cpp
void LayerExDraw::updateRect(RectF &rect) {
    if(updateWhenDraw) {
        tTJSVariant vars[4] = {
            rect.X, rect.Y, rect.Width, rect.Height
        };
        _pUpdate(4, varsp);
    }
}
```

来源：`reference/krkr2/cpp/plugins/layerex_draw/windows/LayerExDraw.cpp:1062-1072`

```cpp
void LayerExDraw::clear(ARGB argb) {
    graphics->Clear(Color(argb));
    ...
    _pUpdate(0, NULL);
}
```

参考语义允许绘制器报告局部更新矩形。Go 实现可映射到 `Layer.markImageDirtyRect`，从而复用现有 GPU 子矩形上传逻辑。

### 8.4 参考插件的变换和公开方法

来源：`reference/krkr2/cpp/plugins/layerex_draw/windows/main.cpp:872-924`

注册属性：

```cpp
NCB_PROPERTY(updateWhenDraw, getUpdateWhenDraw, setUpdateWhenDraw);
NCB_PROPERTY(smoothingMode, getSmoothingMode, setSmoothingMode);
NCB_PROPERTY(textRenderingHint, getTextRenderingHint, setTextRenderingHint);
```

变换方法：

```cpp
setViewTransform
resetViewTransform
rotateViewTransform
scaleViewTransform
translateViewTransform

setTransform
resetTransform
rotateTransform
scaleTransform
translateTransform
```

绘制方法：

```cpp
clear
drawArc
drawPie
drawBezier
drawBeziers
drawClosedCurve
drawClosedCurve2
drawCurve
drawCurve2
drawCurve3
drawEllipse
drawLine
drawLines
drawPolygon
drawRectangle
drawRectangles
drawPathString
drawString
measureString
measureStringInternal
drawImage
drawImageRect
drawImageStretch
drawImageAffine
```

记录功能：

```cpp
record
getRecordImage
redrawRecord
saveRecord
loadRecord
```

这组 API 的规模远大于简单的矩形绘制。完整兼容需要处理 GDI+ / Blend2D 的路径、Pen、Brush、Font、Image、Matrix、裁剪、抗锯齿、文字测量和记录回放语义。

### 8.5 Blend2D 变体的缓冲区条件

来源：`reference/krkr2/cpp/plugins/layerexBase.hpp:135-181`

```cpp
virtual void reset() {
    _width = (GeometryT)_this->GetWidth();
    _height = (GeometryT)_this->GetHeight();
    if(TVPIsSoftwareRenderManager()) {
        _buffer = (BufferT)_this->GetMainImagePixelBufferForWrite();
        _pitch = (PitchT)_this->GetMainImagePixelBufferPitch();
    }
    ...
}
```

来源：`reference/krkr2/cpp/plugins/layerex_draw/blend2d/LayerExDraw.cpp:1507-1539`

```cpp
void LayerExDraw::reset() {
    layerExBase_GL::reset();
    ...
    buffer = _buffer;
    bitmap = new BLImage;
    bitmap->createFromData(
        width, height, BL_FORMAT_PRGB32, buffer, pitch);
    context = new BLContext;
    context->setCompOp(BL_COMP_OP_SRC_OVER);
    ...
}
```

参考 Blend2D 变体在软件渲染管理器下取得 Layer 像素缓冲区。GPU 渲染管理器下 `_buffer` 的取得条件不同，因此参考代码本身也没有提供一个可直接映射到 krkrgo `GPUCompositor` 的通用 `layerExDraw` GPU 命令模型。

## 9. CPU 方案的精确成本模型

设：

- `A`：一次绘制实际触及的像素数量。
- `D`：提交到 GPU 的脏矩形像素数量。
- `W × H`：Layer 图像尺寸。
- `P = W × H × 4`：整张 RGBA 图像字节数。
- `F`：每秒更新次数。

单帧 CPU 方案的主要成本可写成：

```text
CPU 光栅化成本        O(A)
脏矩形合并成本        O(number of draw calls)
透明度预乘成本        O(D) 或 O(P)
GPU 上传数据量        D × 4 bytes 或 P bytes
GPU 合成成本          取决于 Layer 数量、混合模式和目标区域
```

### 9.1 典型完整上传量

| Layer 尺寸 | RGBA 字节数 | 60 FPS 原始上传量 |
| --- | ---: | ---: |
| 1280×720 | 3,686,400 bytes | 221,184,000 bytes/s |
| 1920×1080 | 8,294,400 bytes | 497,664,000 bytes/s |
| 2560×1440 | 14,745,600 bytes | 884,736,000 bytes/s |

换算为二进制 MiB：

| Layer 尺寸 | 每帧 | 60 FPS |
| --- | ---: | ---: |
| 1280×720 | 3.52 MiB | 210.94 MiB/s |
| 1920×1080 | 7.91 MiB | 474.61 MiB/s |
| 2560×1440 | 14.06 MiB | 843.75 MiB/s |

这些数值只描述 `WritePixels` 需要处理的原始像素量。实际耗时还受到 CPU 预乘、Ebitengine 驱动、显卡上传队列、同步点和 GPU 合成阶段影响。

### 9.2 局部上传量

如果一次绘制只修改 `100×100`：

```text
100 × 100 × 4 = 40,000 bytes ≈ 39.06 KiB/frame
39.06 KiB × 60 ≈ 2.29 MiB/s
```

如果一个帧内分别绘制左上角和右下角，脏矩形合并后可能覆盖大部分画布。实现中应保留每次绘制的实际包围矩形，并在 Layer 侧完成矩形合并。

## 10. 三种实现方案的技术比较

### 10.1 方案 A：CPU 光栅化到 Layer.RGBA

数据流：

```text
TJS native method
    -> layerExDraw state
    -> CPU rasterizer
    -> Layer.RGBA
    -> Layer.imageRevision / dirtyRect
    -> RenderPlan.RenderNode.RGBA
    -> GPUCompositor.texture
    -> ebiten.Image.WritePixels
```

需要新增的主要组件：

- LayerExDraw 状态对象。
- Matrix、Pen、Brush、Path、Font、Image 的 TJS 包装。
- CPU 光栅化实现。
- `Layer.markImageDirtyRect` 的精确调用。
- 与现有 Layer 生命周期绑定的释放逻辑。

优势：

- 与 Krkr2 Windows 参考实现的共享像素缓冲区模型一致。
- 复用现有 Layer、RenderPlan、dirty rectangle、GPU texture cache。
- headless CPU compositor 可以观察相同的最终像素。
- API 行为可通过脚本测试和像素测试固定。

风险：

- 大面积动态绘制会增加 CPU 光栅化和 GPU 上传。
- GDI+ / Blend2D 的抗锯齿、路径、文字测量结果需要独立复刻。
- 直接写入 Layer.RGBA 时，透明度格式必须与当前非预乘 RGBA 约定一致。

### 10.2 方案 B：GPU 原生 layerExDraw

需要扩展的区域：

1. `internal/runtime/render_plan.go`
   - 增加 LayerExDraw 专用渲染描述。
   - 定义路径、图片、文字、矩阵、裁剪和混合数据的生命周期。
2. `internal/runtime/layer.go`
   - 增加 GPU 绘制状态和 CPU fallback 状态。
   - 定义状态失效、尺寸变化、Layer 销毁和快照行为。
3. `internal/host/gpu_compositor.go`
   - 增加几何、路径、文字、图片和混合命令的执行器。
   - 增加 GPU 资源缓存、纹理上传、目标纹理和清理逻辑。
4. `internal/host/gpu_shaders.go`
   - 增加抗锯齿、混合、路径填充或图片变换所需 shader。
5. CPU compositor
   - 提供等价像素结果，用于 headless、replay 和 GPU 不可用时的运行。
6. TJS API
   - 处理脚本对象、资源释放、错误传播和属性变化。

优势：

- 大面积动态几何可以减少 CPU 像素写入和整图上传。
- 图片变换和部分混合操作可以保持在 GPU 中。

风险：

- 需要一套完整的后端中间表示。
- GPU 输出与 CPU `Layer.RGBA` 的状态同步需要明确策略。
- 脚本读取像素、保存图片、hit testing、replay 和 GPU fallback 可能触发 readback 或 CPU 重建。
- 参考 API 使用 GDI+ / Blend2D 的路径和文字语义，GPU 版本需要重新定义等价实现。
- 在目标脚本调用规模未知时，工程投入较大。

### 10.3 方案 C：混合表示

一个可行模型：

- 静态图片、文字和局部普通图形写入 `Layer.RGBA`。
- 高频动画或大面积变换使用 GPU 专用描述。
- `RenderPlan` 同时允许 `RGBA`、`RenderMotion` 和新的 `RenderLayerExDraw`。
- CPU compositor 对 `RenderLayerExDraw` 执行软件光栅化。

这个模型能覆盖不同负载，但会同时维护两套绘制语义。CPU 与 GPU 的抗锯齿、混合、裁剪和文字测量差异需要长期维护。

## 11. 当前代码与参考实现之间的关键差异

### 11.1 “GPU 默认”不等于“所有插件绘制都必须 GPU”

krkrgo 的 GPU 模式是宿主最终合成策略。普通 Layer 的脚本绘制状态仍然可以由 CPU 产生，再由 `GPUCompositor` 作为纹理输入。

参考 Krkr2 的 `layerExDraw` Windows 实现直接绑定 Layer 写缓冲区；这一点更接近方案 A。参考 Blend2D 版本的 `layerExBase_GL` 在软件渲染管理器下读取像素缓冲区；它没有提供现成的 `RenderPlan` 风格 GPU 命令对象。

因此，方案选择应基于目标负载和兼容性需求，不能仅从“默认 GPU”推导出“插件必须 GPU 原生化”。

### 11.2 现有 GPU 优化依赖 revision 和 dirty rectangle

GPU capture 模式使用 `Layer.imageRevision` 识别图像变化，GPU 纹理路径根据 `ImageDirty` 决定局部上传。当前 `layerExDraw` 通过 `layerExMarkLocked` 调用 `markImageDirtyRect`，绘制区域会进入 RenderPlan 的 `ImageDirty` 字段；多个调用在 Layer 侧合并为一个上传区域。

### 11.3 现有 `RenderMotion` 不能直接承载 LayerExDraw

`RenderMotion` 针对 MotionPlayer 的源图、矩阵、顶点、索引、混合和 mask。`layerExDraw` 需要处理路径、Pen、Brush、Font、Image、记录回放、裁剪和 `updateWhenDraw`，数据模型不同。复用 `RenderMotion` 会丢失 API 语义。

## 12. 需要模型明确回答的技术问题

请基于当前实现和上述代码引用给出明确结论，并逐项说明理由：

1. `layerExTargetsLocked`、`layerExMarkLocked` 与 `GPUCompositor.texture` 的组合是否保持了局部上传收益。
2. 对 1280×720、1920×1080 Layer 的大面积逐帧图元绘制，CPU 光栅化、透明度预乘和纹理上传的主要耗时占比如何估算。
3. `layerExRasterShape` 的抗锯齿采样、路径点数量、画笔数量会怎样影响单次调用的像素循环次数。
4. 文字路径使用 `fontFace`、`MeasureText`、`RasterizeText` 后再做 CPU 图片混合，和参考 GDI+ `drawString` 的结果与成本有哪些差异。
5. `drawImageAffine`、线性渐变、纹理画刷、PathGradient、虚线画笔的当前实现是否覆盖目标脚本常见调用。
6. `record` 状态同时维护主 Layer 与 record buffer 时，`redrawRecord`、`saveRecord`、`loadRecord` 的复制和编码成本如何变化。
7. 当前 `LayerExObjects`、`LayerExDraws` 的对象生命周期是否覆盖 Layer 销毁、脚本对象失效和多 VM 场景。

请给出：

- 当前实现的功能覆盖判断；
- 适合的 Layer 尺寸和调用频率；
- 最大性能风险；
- 最大兼容性风险；
- 需要从目标游戏补充的一个实际数据；
- 进入 GPU 原生优化前必须测量的指标。

## 13. 代码引用索引

| 主题 | 文件与位置 |
| --- | --- |
| 默认 GPU 选择 | `internal/host/ebiten.go:297-305` |
| GPU Update 流程 | `internal/host/ebiten.go:487-648` |
| GPU 计划合成 | `internal/host/ebiten.go:688-719` |
| GPU capture 标志 | `internal/runtime/session.go:1506-1515` |
| Layer RGBA 状态 | `internal/runtime/layer.go:168-186` |
| Layer 脏矩形 | `internal/runtime/layer.go:252-292` |
| Layer RGBA 写入 | `internal/runtime/layer.go:621-653` |
| RenderNode RGBA | `internal/runtime/render_plan.go:18-44` |
| RenderPlan 捕获 | `internal/runtime/render_plan.go:176-223` |
| GPU capture image revision | `internal/runtime/render_plan.go:382-390` |
| RenderNode dirty rectangle | `internal/runtime/render_plan.go:479-484` |
| GPU 纹理缓存与上传 | `internal/host/gpu_compositor.go:413-443` |
| GPU 预乘上传 | `internal/host/gpu_compositor.go:359-403` |
| Motion GPU 描述 | `internal/runtime/render_plan.go:46-125` |
| C++ LayerExDraw 基类 | `reference/krkr2/cpp/plugins/layerexBase.hpp:71-132` |
| C++ LayerExDraw GL 基类 | `reference/krkr2/cpp/plugins/layerexBase.hpp:135-181` |
| Windows GDI+ 缓冲绑定 | `reference/krkr2/cpp/plugins/layerex_draw/windows/LayerExDraw.cpp:945-977` |
| Windows 局部更新 | `reference/krkr2/cpp/plugins/layerex_draw/windows/LayerExDraw.cpp:908-915` |
| Windows clear/update | `reference/krkr2/cpp/plugins/layerex_draw/windows/LayerExDraw.cpp:1062-1072` |
| LayerExDraw API 注册 | `reference/krkr2/cpp/plugins/layerex_draw/windows/main.cpp:872-924` |
| Blend2D 缓冲绑定 | `reference/krkr2/cpp/plugins/layerex_draw/blend2d/LayerExDraw.cpp:1507-1539` |
| LayerExDraw CPU 实现 | `internal/runtime/layer_ex_draw_native.go` |
| LayerExDraw 状态字段 | `internal/runtime/services.go` 的 `Services.LayerExDraws`、`Services.LayerExObjects` |
| 插件链接接入 | `internal/runtime/services.go` 的 `Plugins.link` 分支 |
| 插件能力索引 | `internal/runtime/plugins.go` 的 `layerexdraw.dll` 条目 |
| Layer 生命周期清理 | `internal/runtime/layer_native.go` 的 `invalidateLayer` |
| Session VM 生命周期清理 | `internal/runtime/session.go` 的 `Session.Close` |
| LayerExDraw API 测试 | `internal/runtime/layer_ex_draw_native_test.go` |
