package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/simd"
)

// drawContext is deliberately allocation-free while walking pixels. The
// compositor allocates its frame once; all blending below is integer arithmetic
// so repeated renders produce identical bytes on every host.
type drawContext struct {
	width, height  int32
	rgba, province []byte
}

func (d *drawContext) drawLayer(l *Layer, parentX, parentY int32) error {
	if l == nil {
		return nil
	}
	return d.drawLayerTo(l, parentX, parentY, l.Type)
}

func (d *drawContext) drawLayerTo(l *Layer, parentX, parentY int32, targetType LayerType) error {
	if l == nil || l.transitionHidden || !l.Visible || l.Opacity == 0 {
		return nil
	}
	if l.Type == LayerEffect || l.Type == LayerFilter {
		return unsupportedLayerType(l)
	}
	ax, ay := parentX+l.Bounds.X, parentY+l.Bounds.Y
	pixels := l.RGBA
	if l.inTransition && len(l.transitionPixels) != 0 {
		pixels = l.transitionPixels
	}
	if len(pixels) != 0 && l.ImageWidth > 0 && l.ImageHeight > 0 {
		deviceWidth, deviceHeight := l.deviceWidth(), l.deviceHeight()
		drawW, drawH := l.Bounds.Width, l.Bounds.Height
		if drawW == 0 {
			drawW = l.ImageWidth
		}
		if drawH == 0 {
			drawH = l.ImageHeight
		}
		localClip := Rect{Width: drawW, Height: drawH}
		if l.clipSet {
			localClip = localClip.Intersect(l.clip)
		}
		provinceMatchesImage := len(l.Province) == int(l.ProvinceWidth*l.ProvinceHeight) && l.ProvinceWidth == l.ImageWidth && l.ProvinceHeight == l.ImageHeight
		// Intersect in source-local coordinates before entering the pixel loops.
		// This matches the DrawDevice cliprect path: off-screen and clipped
		// pixels do not incur per-pixel bounds branches.
		imageLeft, imageTop := l.ImageLeft, l.ImageTop
		x0 := maxInt64(int64(localClip.X), -int64(ax)) - int64(imageLeft)
		y0 := maxInt64(int64(localClip.Y), -int64(ay)) - int64(imageTop)
		x1 := minInt64(int64(localClip.Right()), int64(d.width)-int64(ax)) - int64(imageLeft)
		y1 := minInt64(int64(localClip.Bottom()), int64(d.height)-int64(ay)) - int64(imageTop)
		x0 = maxInt64(x0, 0)
		y0 = maxInt64(y0, 0)
		x1 = minInt64(x1, int64(deviceWidth))
		y1 = minInt64(y1, int64(deviceHeight))
		rowBytes := int(x1-x0) * 4
		opaqueCopy := l.Opacity == 255 && !provinceMatchesImage && (l.Type == LayerOpaque || l.Type == LayerAlpha)
		if l.inTransition && l.transitionOpaque && l.Type == LayerAlpha &&
			l.Opacity == 255 && !provinceMatchesImage && !l.clipSet &&
			ax+imageLeft == 0 && ay+imageTop == 0 &&
			deviceWidth == d.width && deviceHeight == d.height &&
			drawW == d.width && drawH == d.height &&
			x0 == 0 && y0 == 0 && x1 == int64(d.width) && y1 == int64(d.height) &&
			len(pixels) == len(d.rgba) {
			copy(d.rgba, pixels)
		} else if l.Type == LayerAlpha && l.Opacity == 255 {
			if provinceMatchesImage {
				d.drawOpaqueAlphaLayer(pixels, l, targetType, ax, ay, x0, y0, x1, y1)
			} else {
				d.drawOpaqueAlphaLayerNoProvince(pixels, l, targetType, ax, ay, x0, y0, x1, y1)
			}
		} else {
			parallelRows(int(y1)-int(y0), 64, func(rowStart, rowEnd int) {
				for sy := int32(y0) + int32(rowStart); int64(sy) < y0+int64(rowEnd); sy++ {
					localY := imageTop + sy
					dy := ay + localY
					sourceRowOffset := int(sy) * int(deviceWidth) * 4
					destinationRowOffset := int(dy) * int(d.width)
					if opaqueCopy && x0 < x1 {
						sourceStart := sourceRowOffset + int(x0)*4
						destinationX := int(ax) + int(imageLeft) + int(x0)
						destinationStart := (destinationRowOffset + destinationX) * 4
						allAlphaOpaque := l.Type == LayerOpaque
						if l.Type == LayerAlpha {
							if l.inTransition && l.transitionOpaque {
								// beginTransition certifies both crossfade endpoints as
								// opaque, and blendCrossfade preserves that alpha value.
								allAlphaOpaque = true
							} else {
								allAlphaOpaque = rowAllOpaque(pixels[sourceStart : sourceStart+rowBytes])
							}
						}
						if allAlphaOpaque {
							copy(d.rgba[destinationStart:destinationStart+rowBytes], pixels[sourceStart:sourceStart+rowBytes])
							if l.Type == LayerOpaque {
								for alpha := destinationStart + 3; alpha < destinationStart+rowBytes; alpha += 4 {
									d.rgba[alpha] = 255
								}
							}
							continue
						}
					}
					for sx := int32(x0); int64(sx) < x1; sx++ {
						localX := imageLeft + sx
						dx := ax + localX
						si := sourceRowOffset + int(sx)*4
						di := (destinationRowOffset + int(dx)) * 4
						blendPixelForTarget(d.rgba[di:di+4], pixels[si:si+4], clampLayerOpacity(l.Opacity), l.Type, targetType)
						if provinceMatchesImage {
							provinceID := l.Province[(int(l.ImageTop)+int(sy))*int(l.ImageWidth)+int(l.ImageLeft)+int(sx)]
							if provinceID != 0 && pixels[si+3] != 0 {
								d.province[destinationRowOffset+int(dx)] = provinceID
							}
						}
					}
				}
			})
		}
	}
	childTargetType := l.Type
	if l.Type == LayerBinder {
		childTargetType = targetType
	}
	for _, child := range l.children {
		if err := d.drawLayerTo(child, ax, ay, childTargetType); err != nil {
			return err
		}
	}
	return nil
}

func (d *drawContext) drawOpaqueAlphaLayer(pixels []byte, l *Layer, targetType LayerType, ax, ay int32, x0, y0, x1, y1 int64) {
	startX, endX := int(x0), int(x1)
	startY, endY := int(y0), int(y1)
	if startX >= endX || startY >= endY {
		return
	}
	imageWidth := int(l.deviceWidth())
	logicalWidth := int(l.ImageWidth)
	destinationWidth := int(d.width)
	sourceLeft := startX * 4
	rowBytes := (endX - startX) * 4
	destinationX := int(ax) + int(l.ImageLeft) + startX
	parallelRows(endY-startY, 64, func(rowStart, rowEnd int) {
		for sy := startY + rowStart; sy < startY+rowEnd; sy++ {
			source := sy*imageWidth*4 + sourceLeft
			destinationY := int(ay) + int(l.ImageTop) + sy
			destination := (destinationY*destinationWidth + destinationX) * 4
			provinceRow := (int(l.ImageTop) + sy) * logicalWidth
			sourceRow := pixels[source : source+rowBytes]
			if rowAllOpaque(sourceRow) {
				copy(d.rgba[destination:destination+rowBytes], sourceRow)
				d.writeOpaqueProvinceRow(l, startX, endX, provinceRow, destinationY, destinationX)
				continue
			}
			if targetType != LayerOpaque && rowAllOpaque(d.rgba[destination:destination+rowBytes]) {
				simd.AlphaOverOpaqueRow(d.rgba[destination:destination+rowBytes], sourceRow)
				d.writeAlphaProvinceRow(l, sourceRow, startX, endX, provinceRow, destinationY, destinationX)
				continue
			}
			for offset, sx := 0, startX; sx < endX; offset, sx = offset+1, sx+1 {
				si := source + offset*4
				di := destination + offset*4
				srcA := int(pixels[si+3])
				if srcA != 0 {
					if srcA == 255 {
						d.rgba[di], d.rgba[di+1], d.rgba[di+2], d.rgba[di+3] =
							pixels[si], pixels[si+1], pixels[si+2], 255
					} else {
						da := int(d.rgba[di+3])
						if da == 0 && targetType != LayerOpaque {
							d.rgba[di], d.rgba[di+1], d.rgba[di+2], d.rgba[di+3] =
								pixels[si], pixels[si+1], pixels[si+2], byte(srcA)
						} else {
							invA := 255 - srcA
							if targetType == LayerOpaque || da == 255 {
								for c := range 3 {
									d.rgba[di+c] = byte((int(d.rgba[di+c])*invA + int(pixels[si+c])*srcA + 127) / 255)
								}
							} else {
								outA := srcA + da*(255-srcA)/255
								for c := range 3 {
									p := int(pixels[si+c])*srcA + int(d.rgba[di+c])*da*(255-srcA)/255
									d.rgba[di+c] = byte((p*255 + outA*127) / (outA * 255))
								}
							}
							if targetType == LayerOpaque {
								d.rgba[di+3] = byte((da*invA + srcA*srcA + 127) / 255)
							} else if da != 255 {
								d.rgba[di+3] = byte(srcA + da*(255-srcA)/255)
							}
						}
					}
					provinceID := l.Province[provinceRow+int(l.ImageLeft)+sx]
					if provinceID != 0 {
						d.province[destinationY*destinationWidth+destinationX+offset] = provinceID
					}
				}
			}
		}
	})
}

func (d *drawContext) writeOpaqueProvinceRow(l *Layer, sourceStartX, endX, provinceRow, destinationY, destinationX int) {
	provinceBase := provinceRow + int(l.ImageLeft)
	destinationBase := destinationY*int(d.width) + destinationX
	source := l.Province[provinceBase+sourceStartX : provinceBase+endX]
	target := d.province[destinationBase : destinationBase+endX-sourceStartX]
	for offset, id := range source {
		if id != 0 {
			target[offset] = id
		}
	}
}

func (d *drawContext) writeAlphaProvinceRow(l *Layer, source []byte, sourceStartX, endX, provinceRow, destinationY, destinationX int) {
	provinceBase := provinceRow + int(l.ImageLeft)
	destinationBase := destinationY*int(d.width) + destinationX
	for offset := range endX - sourceStartX {
		if source[offset*4+3] == 0 {
			continue
		}
		if id := l.Province[provinceBase+sourceStartX+offset]; id != 0 {
			d.province[destinationBase+offset] = id
		}
	}
}

func (d *drawContext) drawOpaqueAlphaLayerNoProvince(pixels []byte, l *Layer, targetType LayerType, ax, ay int32, x0, y0, x1, y1 int64) {
	startX, endX := int(x0), int(x1)
	startY, endY := int(y0), int(y1)
	if startX >= endX || startY >= endY {
		return
	}
	imageWidth := int(l.deviceWidth())
	destinationWidth := int(d.width)
	rowBytes := (endX - startX) * 4
	sourceLeft := startX * 4
	destinationX := int(ax) + int(l.ImageLeft) + startX
	parallelRows(endY-startY, 64, func(rowStart, rowEnd int) {
		for sy := startY + rowStart; sy < startY+rowEnd; sy++ {
			source := sy*imageWidth*4 + sourceLeft
			destinationY := int(ay) + int(l.ImageTop) + sy
			destination := (destinationY*destinationWidth + destinationX) * 4
			if rowAllOpaque(pixels[source : source+rowBytes]) {
				copy(d.rgba[destination:destination+rowBytes], pixels[source:source+rowBytes])
				continue
			}
			if targetType != LayerOpaque && rowAllOpaque(d.rgba[destination:destination+rowBytes]) {
				simd.AlphaOverOpaqueRow(d.rgba[destination:destination+rowBytes], pixels[source:source+rowBytes])
				continue
			}
			for offset, sx := 0, startX; sx < endX; offset, sx = offset+1, sx+1 {
				si := source + offset*4
				di := destination + offset*4
				srcA := int(pixels[si+3])
				if srcA == 0 {
					continue
				}
				if srcA == 255 {
					d.rgba[di], d.rgba[di+1], d.rgba[di+2], d.rgba[di+3] =
						pixels[si], pixels[si+1], pixels[si+2], 255
					continue
				}
				da := int(d.rgba[di+3])
				if da == 0 && targetType != LayerOpaque {
					d.rgba[di], d.rgba[di+1], d.rgba[di+2], d.rgba[di+3] =
						pixels[si], pixels[si+1], pixels[si+2], byte(srcA)
					continue
				}
				invA := 255 - srcA
				if targetType == LayerOpaque || da == 255 {
					for c := range 3 {
						d.rgba[di+c] = byte((int(d.rgba[di+c])*invA + int(pixels[si+c])*srcA + 127) / 255)
					}
				} else {
					outA := srcA + da*(255-srcA)/255
					for c := range 3 {
						p := int(pixels[si+c])*srcA + int(d.rgba[di+c])*da*(255-srcA)/255
						d.rgba[di+c] = byte((p*255 + outA*127) / (outA * 255))
					}
				}
				if targetType == LayerOpaque {
					d.rgba[di+3] = byte((da*invA + srcA*srcA + 127) / 255)
				} else if da != 255 {
					d.rgba[di+3] = byte(srcA + da*(255-srcA)/255)
				}
			}
		}
	})
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func unsupportedLayerType(l *Layer) error {
	return &layerTypeError{id: l.ID, typ: l.Type}
}

type layerTypeError struct {
	id  LayerID
	typ LayerType
}

func (e *layerTypeError) Error() string {
	return fmt.Sprintf("unsupported layer type %d for layer %d", e.typ, e.id)
}
func (e *layerTypeError) Unwrap() error { return ErrUnsupportedLayerType }

func blendPixel(dst, src []byte, opacity uint8, typ LayerType) {
	blendPixelForTarget(dst, src, opacity, typ, LayerAlpha)
}

func blendPixelForTarget(dst, src []byte, opacity uint8, typ, targetType LayerType) {
	op := int(opacity)
	da := int(dst[3])
	if typ == LayerOpaque {
		if op == 255 {
			dst[0], dst[1], dst[2], dst[3] = src[0], src[1], src[2], 255
			return
		}
		for c := range 3 {
			dst[c] = byte((int(dst[c])*(255-op) + int(src[c])*op + 127) / 255)
		}
		dst[3] = byte(op + da*(255-op)/255)
		return
	}
	if typ == LayerAddAlpha && targetType != LayerAddAlpha {
		// The official bmAddAlphaOnAlpha path remains outside the generic layer compositor.
		return
	}
	if typ == LayerAlpha {
		srcA := int(src[3])
		sa := srcA
		if op != 255 {
			sa = srcA * op / 255
		}
		if sa == 0 {
			return
		}
		if sa == 255 {
			dst[0], dst[1], dst[2], dst[3] = src[0], src[1], src[2], 255
			return
		}
		if targetType == LayerOpaque {
			for c := range 3 {
				dst[c] = byte((int(dst[c])*(255-sa) + int(src[c])*sa + 127) / 255)
			}
			dst[3] = byte((da*(255-sa) + srcA*sa + 127) / 255)
			return
		}
		if da == 0 {
			dst[0], dst[1], dst[2], dst[3] = src[0], src[1], src[2], byte(sa)
			return
		}
		outA := sa + da*(255-sa)/255
		for c := range 3 {
			p := int(src[c])*sa + int(dst[c])*da*(255-sa)/255
			dst[c] = byte((p*255 + outA*127) / (outA * 255))
		}
		dst[3] = byte(outA)
		return
	}
	if typ == LayerAddAlpha {
		sa := int(src[3])
		if op != 255 {
			sa = sa * op / 255
		}
		if sa == 0 {
			return
		}
		if sa == 255 {
			dst[0], dst[1], dst[2], dst[3] = src[0], src[1], src[2], 255
			return
		}
		if da == 0 {
			dst[0], dst[1], dst[2], dst[3] = src[0], src[1], src[2], byte(sa)
			return
		}
		outA := da + sa
		if outA > 255 {
			outA = 255
		}
		for c := range 3 {
			p := int(src[c])*sa + int(dst[c])*da*(255-sa)/255
			dst[c] = byte((p*255 + outA*127) / (outA * 255))
		}
		dst[3] = byte(outA)
		return
	}
	blend := [3]int{int(src[0]), int(src[1]), int(src[2])}
	ps := typ >= LayerPSNormal
	factor := op
	if ps {
		factor = int(src[3]) * op / 255
		if factor == 0 {
			return
		}
	}
	for c := range blend {
		base, source := int(dst[c]), int(src[c])
		switch typ {
		case LayerPSNormal:
			blend[c] = source
		case LayerAdditive, LayerPSAdditive:
			blend[c] = minByte(base+source, 255)
		case LayerSubtractive, LayerPSSubtractive:
			blend[c] = maxByte(base-source, 0)
		case LayerMultiplicative, LayerPSMultiplicative:
			blend[c] = base * source / 255
		case LayerScreen, LayerPSScreen:
			blend[c] = 255 - (255-base)*(255-source)/255
		case LayerDodge, LayerPSColorDodge, LayerPSColorDodge5:
			blend[c] = colorDodge(base, source)
		case LayerDarken, LayerPSDarken:
			blend[c] = minByte(base, source)
		case LayerLighten, LayerPSLighten:
			blend[c] = maxByte(base, source)
		case LayerPSOverlay:
			blend[c] = overlay(base, source)
		case LayerPSHardLight:
			blend[c] = overlay(source, base)
		case LayerPSSoftLight:
			blend[c] = softLight(base, source)
		case LayerPSColorBurn:
			blend[c] = colorBurn(base, source)
		case LayerPSDifference, LayerPSDifference5:
			blend[c] = absInt(base - source)
		case LayerPSExclusion:
			blend[c] = base + source - 2*base*source/255
		default:
			return
		}
	}
	for c := range 3 {
		dst[c] = byte((int(dst[c])*(255-factor) + blend[c]*factor + 127) / 255)
	}
	if !usesAlphaTarget(targetType) {
		dst[3] = byte((da*(255-factor) + int(src[3])*factor + 127) / 255)
	}
}

func usesAlphaTarget(typ LayerType) bool {
	switch typ {
	case LayerAlpha, LayerAddAlpha, LayerPSNormal, LayerPSAdditive, LayerPSSubtractive,
		LayerPSMultiplicative, LayerPSScreen, LayerPSOverlay, LayerPSHardLight,
		LayerPSSoftLight, LayerPSColorDodge, LayerPSColorDodge5, LayerPSColorBurn,
		LayerPSLighten, LayerPSDarken, LayerPSDifference, LayerPSDifference5,
		LayerPSExclusion:
		return true
	default:
		return false
	}
}

func minByte(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxByte(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func colorDodge(a, b int) int {
	if b >= 255 {
		return 255
	}
	v := a * 255 / (255 - b)
	if v > 255 {
		return 255
	}
	return v
}
func colorBurn(a, b int) int {
	if b <= 0 {
		return 0
	}
	v := 255 - (255-a)*255/b
	if v < 0 {
		return 0
	}
	return v
}
func overlay(base, blend int) int {
	if base < 128 {
		return 2 * base * blend / 255
	}
	return 255 - 2*(255-base)*(255-blend)/255
}
func softLight(base, blend int) int { return (base*(255-blend) + overlay(base, blend)*blend) / 255 }
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func hitLayer(l *Layer, x, y, parentX, parentY int32) *Layer {
	if l == nil || !l.Visible || !l.Enabled || l.Opacity == 0 {
		return nil
	}
	ax, ay := parentX+l.Bounds.X, parentY+l.Bounds.Y
	for i := len(l.children) - 1; i >= 0; i-- {
		if hit := hitLayer(l.children[i], x, y, ax, ay); hit != nil {
			return hit
		}
	}
	if !layerHitAt(l, x, y, ax, ay) {
		return nil
	}
	return l
}

func layerHitAt(l *Layer, x, y, absoluteX, absoluteY int32) bool {
	if l.EventTransparent {
		return false
	}
	bounds := Rect{X: absoluteX, Y: absoluteY, Width: l.Bounds.Width, Height: l.Bounds.Height}
	if !bounds.Contains(x, y) {
		return false
	}
	localX, localY := x-absoluteX, y-absoluteY
	if l.clipSet && !l.clip.Contains(localX, localY) {
		return false
	}
	sx, sy := localX-l.ImageLeft, localY-l.ImageTop
	if sx < 0 || sy < 0 || sx >= l.ImageWidth || sy >= l.ImageHeight || len(l.RGBA) == 0 {
		return false
	}
	px, py := sx, sy
	pixelIndex := int(py*l.deviceWidth() + px)
	if l.Hit == HitProvince {
		idx := int(sy*l.ImageWidth + sx)
		return l.ProvinceWidth == l.ImageWidth && l.ProvinceHeight == l.ImageHeight &&
			idx < len(l.Province) && l.Province[idx] != 0
	}
	return int(l.RGBA[pixelIndex*4+3])*int(l.Opacity)/255 >= l.HitThreshold
}

// HitTest performs the same foremost-child search as Compositor.HitTest, but
// starts at this layer and accepts coordinates in the containing space.
func (l *Layer) HitTest(x, y int32) *Layer { return hitLayer(l, x, y, 0, 0) }
