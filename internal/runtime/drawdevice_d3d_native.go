package runtime

// drawdeviceD3D compatibility surface.
//
// The Neko package executes system/d3d.tjs unconditionally before it decides
// whether the Direct3D draw device is active. That script derives D3DRefImage
// from D3DImage and D3DAffineLayer from D3DLayer, so both native classes must
// exist even when the game keeps the software draw device. The classes below
// back those names with CPU image, picture, layer, and device state. Rendering
// is performed by the runtime's existing integer RGBA blender, which keeps the
// observable pixel results deterministic and independent of Direct3D.

import (
	"fmt"
	"math"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const (
	d3dMaxDimension = 16384
	d3dMaxPixels    = 64 * 1024 * 1024
)

const (
	d3dDrawPlaneFront = int32(0)
	d3dDrawPlaneBack  = int32(1)
	d3dDrawPlaneBoth  = int32(2)
)

type d3dImageData struct {
	width, height int32
	rgba          []byte
}

func newD3DImageData(width, height int32) (*d3dImageData, error) {
	if width < 0 || height < 0 || width > d3dMaxDimension || height > d3dMaxDimension ||
		uint64(width)*uint64(height) > d3dMaxPixels {
		return nil, fmt.Errorf("D3DImage: invalid dimensions %dx%d", width, height)
	}
	return &d3dImageData{width: width, height: height, rgba: make([]byte, int(width)*int(height)*4)}, nil
}

func (im *d3dImageData) valid() bool {
	if im == nil || im.width < 0 || im.height < 0 {
		return false
	}
	return len(im.rgba) == int(im.width)*int(im.height)*4
}

func (im *d3dImageData) set(width, height int32, rgba []byte) error {
	if !validLayerImageSize(width, height, len(rgba), 4) {
		return fmt.Errorf("D3DImage: invalid RGBA payload for %dx%d", width, height)
	}
	im.width, im.height = width, height
	im.rgba = append(im.rgba[:0], rgba...)
	return nil
}

func rgbaFromLayer(l *Layer) (int32, int32, []byte, error) {
	if l == nil {
		return 0, 0, nil, fmt.Errorf("D3DImage.load: source layer is unavailable")
	}
	if err := validLayerPixels(l); err != nil {
		return 0, 0, nil, fmt.Errorf("D3DImage.load: %w", err)
	}
	width, height := l.ImageWidth, l.ImageHeight
	if width <= 0 || height <= 0 {
		return 0, 0, nil, fmt.Errorf("D3DImage.load: layer has no image")
	}
	return width, height, l.RGBA, nil
}

func rgbaFromBitmap(b *Bitmap) (int32, int32, []byte, error) {
	if b == nil {
		return 0, 0, nil, fmt.Errorf("D3DImage.load: source bitmap is unavailable")
	}
	if b.Width == 0 || b.Height == 0 || !validLayerImageSize(int32(b.deviceWidth()), int32(b.deviceHeight()), len(b.RGBA), 4) {
		return 0, 0, nil, fmt.Errorf("D3DImage.load: bitmap has no image")
	}
	return int32(b.Width), int32(b.Height), b.RGBA, nil
}

type d3dPictureData struct {
	layerID, imageID                            tjs.ObjectID
	sourceX, sourceY, sourceWidth, sourceHeight int32
	destX, destY                                int32
	coordX, coordY                              float64
	opacity                                     float64
	blendMode                                   LayerType
}

func newD3DPictureData(layerID, imageID tjs.ObjectID) *d3dPictureData {
	return &d3dPictureData{
		layerID: layerID, imageID: imageID,
		opacity: 255, blendMode: LayerAlpha,
	}
}

type d3dLayerData struct {
	deviceID                    tjs.ObjectID
	pictures                    []tjs.ObjectID
	textureWidth, textureHeight int32
	texture                     []byte
	visible                     bool
	drawPlane                   int32
	frontIndex, backIndex       int32
	opacity                     float64
	matrix                      [16]float64
	clip                        Rect
	clipSet                     bool
	order                       uint64
}

func newD3DLayerData(deviceID tjs.ObjectID, order uint64) *d3dLayerData {
	layer := &d3dLayerData{
		deviceID: deviceID,
		visible:  true,
		opacity:  255,
		order:    order,
	}
	layer.matrix[0] = 1
	layer.matrix[5] = 1
	layer.matrix[10] = 1
	layer.matrix[15] = 1
	return layer
}

type d3dDeviceData struct {
	width, height               int32
	primaryWidth, primaryHeight int32
	screenX, screenY            int32
	screenWidth, screenHeight   int32
	offsetX, offsetY            int32
	clearColor                  uint32
	transState                  float64
	layerManagerIndex           int32
	stretchType                 int32
	bicubicParam                float64
	layers                      []tjs.ObjectID
	layersOrder                 uint64
	frame                       []byte
	transitionSource            []byte
	inTransition                bool
}

func newD3DDeviceData(width, height int32) (*d3dDeviceData, error) {
	if width < 0 || height < 0 || width > d3dMaxDimension || height > d3dMaxDimension ||
		uint64(width)*uint64(height) > d3dMaxPixels {
		return nil, fmt.Errorf("D3D: invalid dimensions %dx%d", width, height)
	}
	return &d3dDeviceData{
		width: width, height: height,
		primaryWidth: width, primaryHeight: height,
		clearColor:   0xff000000,
		bicubicParam: 0.5,
	}, nil
}

func (d *d3dDeviceData) frameWidth() int32 {
	if d == nil {
		return 0
	}
	if d.primaryWidth > 0 {
		return d.primaryWidth
	}
	return d.width
}

func (d *d3dDeviceData) frameHeight() int32 {
	if d == nil {
		return 0
	}
	if d.primaryHeight > 0 {
		return d.primaryHeight
	}
	return d.height
}

func d3dContextID(context tjs.Value, operation string) (tjs.ObjectID, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() || context.ObjectRef().Object == 0 {
		return 0, fmt.Errorf("%s requires a non-null object context", operation)
	}
	return context.ObjectRef().Object, nil
}

func d3dObjectID(value tjs.Value) (tjs.ObjectID, bool) {
	if value.Kind() != tjs.KindObject || value.IsNull() || value.ObjectRef().Object == 0 {
		return 0, false
	}
	return value.ObjectRef().Object, true
}

func d3dIntArg(args []tjs.Value, index int, operation string) (int32, error) {
	if index < 0 || index >= len(args) {
		return 0, fmt.Errorf("%s: missing argument %d", operation, index)
	}
	n, err := args[index].Int()
	if err != nil {
		return 0, err
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("%s: argument %d is outside the 32-bit range", operation, index)
	}
	return int32(n), nil
}

func d3dUint32Arg(args []tjs.Value, index int, operation string) (uint32, error) {
	if index < 0 || index >= len(args) {
		return 0, fmt.Errorf("%s: missing argument %d", operation, index)
	}
	n, err := args[index].Int()
	if err != nil {
		return 0, err
	}
	if n < 0 || n > math.MaxUint32 {
		return 0, fmt.Errorf("%s: argument %d is outside the 32-bit unsigned range", operation, index)
	}
	return uint32(n), nil
}

func d3dFloatArg(args []tjs.Value, index int, operation string) (float64, error) {
	if index < 0 || index >= len(args) {
		return 0, fmt.Errorf("%s: missing argument %d", operation, index)
	}
	return args[index].Float()
}

func d3dOpacity(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return value
}

func d3dBoolean(value tjs.Value) bool {
	ok, err := value.Truth()
	return err == nil && ok
}

func d3dClearColorRGBA(color uint32, width, height int32) []byte {
	pixels := make([]byte, int(width)*int(height)*4)
	for i := 0; i+3 < len(pixels); i += 4 {
		pixels[i] = byte(color >> 16)
		pixels[i+1] = byte(color >> 8)
		pixels[i+2] = byte(color)
		pixels[i+3] = byte(color >> 24)
	}
	return pixels
}

func (s *Services) removeD3DImage(id tjs.ObjectID) {
	s.mu.Lock()
	delete(s.D3DImages, id)
	s.mu.Unlock()
}

func (s *Services) removeD3DPicture(id tjs.ObjectID) {
	s.mu.Lock()
	picture := s.D3DPictures[id]
	delete(s.D3DPictures, id)
	if picture != nil {
		if layer := s.D3DLayers[picture.layerID]; layer != nil {
			for i, current := range layer.pictures {
				if current == id {
					copy(layer.pictures[i:], layer.pictures[i+1:])
					layer.pictures = layer.pictures[:len(layer.pictures)-1]
					break
				}
			}
		}
	}
	s.mu.Unlock()
}

func (s *Services) removeD3DLayer(id tjs.ObjectID) {
	s.mu.Lock()
	layer := s.D3DLayers[id]
	delete(s.D3DLayers, id)
	if layer != nil {
		if device := s.D3DDevices[layer.deviceID]; device != nil {
			for i, current := range device.layers {
				if current == id {
					copy(device.layers[i:], device.layers[i+1:])
					device.layers = device.layers[:len(device.layers)-1]
					break
				}
			}
		}
		for _, pictureID := range layer.pictures {
			delete(s.D3DPictures, pictureID)
		}
	}
	s.mu.Unlock()
}

func (s *Services) removeD3DDevice(id tjs.ObjectID) {
	s.mu.Lock()
	delete(s.D3DDevices, id)
	s.mu.Unlock()
}

func (s *Services) d3dImageNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	id, err := d3dContextID(context, "D3DImage."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	switch operation {
	case "@initialize":
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		s.removeD3DImage(id)
		return tjs.VoidValue, nil
	case "D3DImage":
		image, err := newD3DImageData(0, 0)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		s.D3DImages[id] = image
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "load":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("D3DImage.load: missing source")
		}
		var width, height int32
		var pixels []byte
		s.mu.RLock()
		layer := (*Layer)(nil)
		bitmap := (*Bitmap)(nil)
		image := (*d3dImageData)(nil)
		if sourceID, ok := d3dObjectID(args[0]); ok {
			layer = s.Layers[sourceID]
			bitmap = s.Bitmaps[sourceID]
			image = s.D3DImages[sourceID]
		}
		s.mu.RUnlock()
		switch {
		case layer != nil:
			width, height, pixels, err = rgbaFromLayer(layer)
		case bitmap != nil:
			width, height, pixels, err = rgbaFromBitmap(bitmap)
		case image != nil:
			if !image.valid() {
				return tjs.VoidValue, fmt.Errorf("D3DImage.load: source image is invalid")
			}
			width, height, pixels = image.width, image.height, image.rgba
		default:
			return tjs.VoidValue, fmt.Errorf("D3DImage.load: source must be a Layer, Bitmap, or D3DImage")
		}
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		destination := s.D3DImages[id]
		if destination == nil {
			destination, err = newD3DImageData(0, 0)
			if err != nil {
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
			s.D3DImages[id] = destination
		}
		err = destination.set(width, height, pixels)
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "get:width":
		s.mu.RLock()
		image := s.D3DImages[id]
		s.mu.RUnlock()
		if image == nil {
			return tjs.IntegerValue(0), nil
		}
		return tjs.IntegerValue(int64(image.width)), nil
	case "get:height":
		s.mu.RLock()
		image := s.D3DImages[id]
		s.mu.RUnlock()
		if image == nil {
			return tjs.IntegerValue(0), nil
		}
		return tjs.IntegerValue(int64(image.height)), nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: D3DImage.%s", ErrUnsupportedNative, operation)
	}
}

func (s *Services) d3dPictureNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	id, err := d3dContextID(context, "D3DPicture."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	switch operation {
	case "@initialize":
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		s.removeD3DPicture(id)
		return tjs.VoidValue, nil
	case "D3DPicture":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("D3DPicture: layer and image are required")
		}
		layerID, ok := d3dObjectID(args[0])
		if !ok {
			return tjs.VoidValue, fmt.Errorf("D3DPicture: first argument must be a D3DLayer")
		}
		imageID, ok := d3dObjectID(args[1])
		if !ok {
			return tjs.VoidValue, fmt.Errorf("D3DPicture: second argument must be a D3DImage")
		}
		s.mu.Lock()
		layer := s.D3DLayers[layerID]
		image := s.D3DImages[imageID]
		if layer == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("D3DPicture: first argument is not a D3DLayer")
		}
		if image == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("D3DPicture: second argument is not a D3DImage")
		}
		picture := newD3DPictureData(layerID, imageID)
		picture.sourceWidth, picture.sourceHeight = image.width, image.height
		s.D3DPictures[id] = picture
		layer.pictures = append(layer.pictures, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "assignImageRange":
		if len(args) < 6 {
			return tjs.VoidValue, fmt.Errorf("D3DPicture.assignImageRange: six arguments are required")
		}
		var values [6]int32
		for i := range values {
			values[i], err = d3dIntArg(args, i, "D3DPicture.assignImageRange")
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		s.mu.Lock()
		picture := s.D3DPictures[id]
		if picture == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("D3DPicture.assignImageRange: picture is unavailable")
		}
		image := s.D3DImages[picture.imageID]
		if image == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("D3DPicture.assignImageRange: picture image is unavailable")
		}
		if values[0] < 0 || values[1] < 0 || values[2] < 0 || values[3] < 0 ||
			int64(values[0])+int64(values[2]) > int64(image.width) ||
			int64(values[1])+int64(values[3]) > int64(image.height) {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("D3DPicture.assignImageRange: source range %d,%d %dx%d is outside %dx%d",
				values[0], values[1], values[2], values[3], image.width, image.height)
		}
		picture.sourceX, picture.sourceY, picture.sourceWidth, picture.sourceHeight = values[0], values[1], values[2], values[3]
		picture.destX, picture.destY = values[4], values[5]
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "clearImageRange":
		s.mu.Lock()
		picture := s.D3DPictures[id]
		if picture == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("D3DPicture.clearImageRange: picture is unavailable")
		}
		if len(args) >= 6 {
			x, err := d3dIntArg(args, 0, "D3DPicture.clearImageRange")
			if err != nil {
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
			y, err := d3dIntArg(args, 1, "D3DPicture.clearImageRange")
			if err != nil {
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
			w, err := d3dIntArg(args, 2, "D3DPicture.clearImageRange")
			if err != nil {
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
			h, err := d3dIntArg(args, 3, "D3DPicture.clearImageRange")
			if err != nil {
				s.mu.Unlock()
				return tjs.VoidValue, err
			}
			layer := s.D3DLayers[picture.layerID]
			if layer != nil && layer.textureWidth > 0 && layer.textureHeight > 0 {
				s.clearD3DPictureRangeLocked(layer, x, y, w, h)
			}
			s.mu.Unlock()
			return tjs.VoidValue, nil
		}
		if image := s.D3DImages[picture.imageID]; image != nil {
			picture.sourceX, picture.sourceY = 0, 0
			picture.sourceWidth, picture.sourceHeight = image.width, image.height
			picture.destX, picture.destY = 0, 0
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "setCoord":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("D3DPicture.setCoord: two arguments are required")
		}
		x, err := d3dFloatArg(args, 0, "D3DPicture.setCoord")
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := d3dFloatArg(args, 1, "D3DPicture.setCoord")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		if picture := s.D3DPictures[id]; picture != nil {
			picture.coordX, picture.coordY = x, y
		} else {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("D3DPicture.setCoord: picture is unavailable")
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "get:opacity":
		s.mu.RLock()
		picture := s.D3DPictures[id]
		s.mu.RUnlock()
		if picture == nil {
			return tjs.RealValue(0), nil
		}
		return tjs.RealValue(picture.opacity), nil
	case "set:opacity":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("D3DPicture.opacity: missing value")
		}
		value, err := args[0].Float()
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		picture := s.D3DPictures[id]
		if picture != nil {
			picture.opacity = d3dOpacity(value)
		}
		s.mu.Unlock()
		if picture == nil {
			return tjs.VoidValue, fmt.Errorf("D3DPicture.opacity: picture is unavailable")
		}
		return tjs.VoidValue, nil
	case "get:blendMode":
		s.mu.RLock()
		picture := s.D3DPictures[id]
		s.mu.RUnlock()
		if picture == nil {
			return tjs.IntegerValue(int64(LayerAlpha)), nil
		}
		return tjs.IntegerValue(int64(picture.blendMode)), nil
	case "set:blendMode":
		value, err := d3dIntArg(args, 0, "D3DPicture.blendMode")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		picture := s.D3DPictures[id]
		if picture != nil {
			picture.blendMode = LayerType(value)
		}
		s.mu.Unlock()
		if picture == nil {
			return tjs.VoidValue, fmt.Errorf("D3DPicture.blendMode: picture is unavailable")
		}
		return tjs.VoidValue, nil
	case "draw":
		s.mu.Lock()
		picture := s.D3DPictures[id]
		if picture == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("D3DPicture.draw: picture is unavailable")
		}
		layer := s.D3DLayers[picture.layerID]
		device := (*d3dDeviceData)(nil)
		if layer != nil {
			device = s.D3DDevices[layer.deviceID]
		}
		if layer != nil && device != nil {
			s.composeD3DLayerTextureLocked(layer, device)
		}
		s.mu.Unlock()
		if layer == nil || device == nil {
			return tjs.VoidValue, fmt.Errorf("D3DPicture.draw: picture layer or device is unavailable")
		}
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: D3DPicture.%s", ErrUnsupportedNative, operation)
	}
}

func (s *Services) d3dLayerNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	id, err := d3dContextID(context, "D3DLayer."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	switch operation {
	case "@initialize":
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		s.removeD3DLayer(id)
		return tjs.VoidValue, nil
	case "D3DLayer":
		deviceID := tjs.ObjectID(0)
		if len(args) > 0 && args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
			value, ok := d3dObjectID(args[0])
			if !ok {
				return tjs.VoidValue, fmt.Errorf("D3DLayer: first argument must be a D3D device")
			}
			deviceID = value
		}
		s.mu.Lock()
		device := s.D3DDevices[deviceID]
		if deviceID != 0 && device == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("D3DLayer: first argument is not a D3D device")
		}
		order := uint64(0)
		if device != nil {
			device.layersOrder++
			order = device.layersOrder
		}
		layer := newD3DLayerData(deviceID, order)
		s.D3DLayers[id] = layer
		if device != nil {
			device.layers = append(device.layers, id)
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "setMatrix":
		if len(args) < 16 {
			return tjs.VoidValue, fmt.Errorf("D3DLayer.setMatrix: sixteen arguments are required")
		}
		var matrix [16]float64
		for i := range matrix {
			matrix[i], err = d3dFloatArg(args, i, "D3DLayer.setMatrix")
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		s.mu.Lock()
		layer := s.D3DLayers[id]
		if layer != nil {
			layer.matrix = matrix
		}
		s.mu.Unlock()
		if layer == nil {
			return tjs.VoidValue, fmt.Errorf("D3DLayer.setMatrix: layer is unavailable")
		}
		return tjs.VoidValue, nil
	case "setClip":
		if len(args) < 4 {
			return tjs.VoidValue, fmt.Errorf("D3DLayer.setClip: four arguments are required")
		}
		x, err := d3dFloatArg(args, 0, "D3DLayer.setClip")
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := d3dFloatArg(args, 1, "D3DLayer.setClip")
		if err != nil {
			return tjs.VoidValue, err
		}
		w, err := d3dFloatArg(args, 2, "D3DLayer.setClip")
		if err != nil {
			return tjs.VoidValue, err
		}
		h, err := d3dFloatArg(args, 3, "D3DLayer.setClip")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		layer := s.D3DLayers[id]
		if layer != nil {
			layer.clip = Rect{X: int32(math.Round(x)), Y: int32(math.Round(y)),
				Width: int32(math.Round(w)), Height: int32(math.Round(h))}
			layer.clipSet = true
		}
		s.mu.Unlock()
		if layer == nil {
			return tjs.VoidValue, fmt.Errorf("D3DLayer.setClip: layer is unavailable")
		}
		return tjs.VoidValue, nil
	case "draw", "update":
		s.mu.Lock()
		layer := s.D3DLayers[id]
		var device *d3dDeviceData
		if layer != nil {
			device = s.D3DDevices[layer.deviceID]
		}
		if layer != nil && device != nil {
			s.composeD3DLayerTextureLocked(layer, device)
			if operation == "update" {
				s.composeD3DDeviceFrameLocked(device)
			}
		}
		s.mu.Unlock()
		if layer == nil || device == nil {
			return tjs.VoidValue, fmt.Errorf("D3DLayer.%s: layer or device is unavailable", operation)
		}
		return tjs.VoidValue, nil
	case "get:drawPlane":
		s.mu.RLock()
		layer := s.D3DLayers[id]
		s.mu.RUnlock()
		if layer == nil {
			return tjs.IntegerValue(int64(d3dDrawPlaneFront)), nil
		}
		return tjs.IntegerValue(int64(layer.drawPlane)), nil
	case "set:drawPlane":
		value, err := d3dIntArg(args, 0, "D3DLayer.drawPlane")
		if err != nil {
			return tjs.VoidValue, err
		}
		if value < d3dDrawPlaneFront || value > d3dDrawPlaneBoth {
			return tjs.VoidValue, fmt.Errorf("D3DLayer.drawPlane: invalid plane %d", value)
		}
		s.mu.Lock()
		layer := s.D3DLayers[id]
		if layer != nil {
			layer.drawPlane = value
		}
		s.mu.Unlock()
		if layer == nil {
			return tjs.VoidValue, fmt.Errorf("D3DLayer.drawPlane: layer is unavailable")
		}
		return tjs.VoidValue, nil
	case "get:visible":
		s.mu.RLock()
		layer := s.D3DLayers[id]
		s.mu.RUnlock()
		if layer == nil {
			return tjs.IntegerValue(0), nil
		}
		if layer.visible {
			return tjs.IntegerValue(1), nil
		}
		return tjs.IntegerValue(0), nil
	case "set:visible":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("D3DLayer.visible: missing value")
		}
		visible := d3dBoolean(args[0])
		s.mu.Lock()
		layer := s.D3DLayers[id]
		if layer != nil {
			layer.visible = visible
		}
		s.mu.Unlock()
		if layer == nil {
			return tjs.VoidValue, fmt.Errorf("D3DLayer.visible: layer is unavailable")
		}
		return tjs.VoidValue, nil
	case "get:frontIndex", "get:backIndex":
		s.mu.RLock()
		layer := s.D3DLayers[id]
		s.mu.RUnlock()
		if layer == nil {
			return tjs.IntegerValue(0), nil
		}
		if operation == "get:frontIndex" {
			return tjs.IntegerValue(int64(layer.frontIndex)), nil
		}
		return tjs.IntegerValue(int64(layer.backIndex)), nil
	case "set:frontIndex", "set:backIndex":
		value, err := d3dIntArg(args, 0, "D3DLayer."+operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		layer := s.D3DLayers[id]
		if layer != nil {
			if operation == "set:frontIndex" {
				layer.frontIndex = value
			} else {
				layer.backIndex = value
			}
		}
		s.mu.Unlock()
		if layer == nil {
			return tjs.VoidValue, fmt.Errorf("D3DLayer.%s: layer is unavailable", operation)
		}
		return tjs.VoidValue, nil
	case "get:opacity":
		s.mu.RLock()
		layer := s.D3DLayers[id]
		s.mu.RUnlock()
		if layer == nil {
			return tjs.RealValue(0), nil
		}
		return tjs.RealValue(layer.opacity), nil
	case "set:opacity":
		value, err := d3dFloatArg(args, 0, "D3DLayer.opacity")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		layer := s.D3DLayers[id]
		if layer != nil {
			layer.opacity = d3dOpacity(value)
		}
		s.mu.Unlock()
		if layer == nil {
			return tjs.VoidValue, fmt.Errorf("D3DLayer.opacity: layer is unavailable")
		}
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: D3DLayer.%s", ErrUnsupportedNative, operation)
	}
}

func (s *Services) clearD3DPictureRangeLocked(layer *d3dLayerData, x, y, width, height int32) {
	if layer == nil || width <= 0 || height <= 0 || layer.textureWidth <= 0 || layer.textureHeight <= 0 {
		return
	}
	x0, y0 := int64(x), int64(y)
	x1, y1 := x0+int64(width), y0+int64(height)
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > int64(layer.textureWidth) {
		x1 = int64(layer.textureWidth)
	}
	if y1 > int64(layer.textureHeight) {
		y1 = int64(layer.textureHeight)
	}
	for row := y0; row < y1; row++ {
		start := (row*int64(layer.textureWidth) + x0) * 4
		end := (row*int64(layer.textureWidth) + x1) * 4
		clear(layer.texture[start:end])
	}
}

func (s *Services) composeD3DLayerTextureLocked(layer *d3dLayerData, device *d3dDeviceData) {
	if layer == nil || device == nil {
		return
	}
	width, height := device.frameWidth(), device.frameHeight()
	if width <= 0 || height <= 0 {
		return
	}
	required := int(width) * int(height) * 4
	if layer.textureWidth != width || layer.textureHeight != height || len(layer.texture) != required {
		layer.textureWidth, layer.textureHeight = width, height
		layer.texture = make([]byte, required)
	} else {
		clear(layer.texture)
	}
	for _, pictureID := range layer.pictures {
		picture := s.D3DPictures[pictureID]
		if picture == nil {
			continue
		}
		image := s.D3DImages[picture.imageID]
		if image == nil || !image.valid() || image.width <= 0 || image.height <= 0 {
			continue
		}
		s.drawD3DPictureLocked(layer.texture, width, height, picture, image)
	}
}

func (s *Services) drawD3DPictureLocked(destination []byte, width, height int32, picture *d3dPictureData, image *d3dImageData) {
	sourceX, sourceY := picture.sourceX, picture.sourceY
	sourceWidth, sourceHeight := picture.sourceWidth, picture.sourceHeight
	if sourceWidth <= 0 || sourceHeight <= 0 {
		sourceWidth, sourceHeight = image.width, image.height
	}
	destX := picture.destX + int32(math.Round(picture.coordX))
	destY := picture.destY + int32(math.Round(picture.coordY))
	opacity := uint8(math.Round(picture.opacity))
	if opacity == 0 {
		return
	}
	for row := int32(0); row < sourceHeight; row++ {
		dy := destY + row
		if dy < 0 || dy >= height {
			continue
		}
		sy := sourceY + row
		if sy < 0 || sy >= image.height {
			continue
		}
		for column := int32(0); column < sourceWidth; column++ {
			dx := destX + column
			if dx < 0 || dx >= width {
				continue
			}
			sx := sourceX + column
			if sx < 0 || sx >= image.width {
				continue
			}
			si := (int(sy)*int(image.width) + int(sx)) * 4
			di := (int(dy)*int(width) + int(dx)) * 4
			blendPixel(destination[di:di+4], image.rgba[si:si+4], opacity, picture.blendMode)
		}
	}
}

func (s *Services) composeD3DDeviceFrameLocked(device *d3dDeviceData) {
	if device == nil {
		return
	}
	width, height := device.frameWidth(), device.frameHeight()
	if width <= 0 || height <= 0 {
		device.frame = nil
		return
	}
	required := int(width) * int(height) * 4
	if len(device.frame) != required {
		device.frame = make([]byte, required)
	}
	copy(device.frame, d3dClearColorRGBA(device.clearColor, width, height))
	for _, plane := range []int32{d3dDrawPlaneBack, d3dDrawPlaneFront, d3dDrawPlaneBoth} {
		for _, layerID := range device.layers {
			layer := s.D3DLayers[layerID]
			if layer == nil || !layer.visible || layer.opacity == 0 || layer.drawPlane != plane {
				continue
			}
			s.composeD3DLayerTextureLocked(layer, device)
			s.blendD3DLayerIntoFrame(device, layer, width, height)
		}
	}
	if device.inTransition && len(device.transitionSource) == required && device.transState > 0 {
		state := device.transState
		if state > 1 {
			state = 1
		}
		for i := 0; i < required; i += 4 {
			for c := 0; c < 4; c++ {
				old := int(device.transitionSource[i+c])
				current := int(device.frame[i+c])
				device.frame[i+c] = byte((old*int(math.Round(state*255)) + current*(255-int(math.Round(state*255))) + 127) / 255)
			}
		}
	}
}

func (s *Services) blendD3DLayerIntoFrame(device *d3dDeviceData, layer *d3dLayerData, width, height int32) {
	if layer == nil || len(layer.texture) != int(width)*int(height)*4 {
		return
	}
	m := layer.matrix
	a, b, c, d := m[0], m[4], m[1], m[5]
	tx, ty := m[12], m[13]
	det := a*d - b*c
	if math.Abs(det) < 1e-12 {
		return
	}
	opacity := uint8(math.Round(layer.opacity))
	if opacity == 0 {
		return
	}
	minX, minY := float64(width), float64(height)
	maxX, maxY := 0.0, 0.0
	for _, corner := range [][2]float64{{0, 0}, {float64(width), 0}, {0, float64(height)}, {float64(width), float64(height)}} {
		x := a*corner[0] + b*corner[1] + tx
		y := c*corner[0] + d*corner[1] + ty
		if x < minX {
			minX = x
		}
		if y < minY {
			minY = y
		}
		if x > maxX {
			maxX = x
		}
		if y > maxY {
			maxY = y
		}
	}
	x0 := int32(math.Floor(minX))
	y0 := int32(math.Floor(minY))
	x1 := int32(math.Ceil(maxX))
	y1 := int32(math.Ceil(maxY))
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > width {
		x1 = width
	}
	if y1 > height {
		y1 = height
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			sx := (d*(fx-tx) - b*(fy-ty)) / det
			sy := (-c*(fx-tx) + a*(fy-ty)) / det
			textureX, textureY := int32(math.Floor(sx)), int32(math.Floor(sy))
			if textureX < 0 || textureY < 0 || textureX >= width || textureY >= height {
				continue
			}
			if layer.clipSet && !layer.clip.Contains(textureX, textureY) {
				continue
			}
			si := (int(textureY)*int(width) + int(textureX)) * 4
			di := (int(y)*int(width) + int(x)) * 4
			blendPixel(device.frame[di:di+4], layer.texture[si:si+4], opacity, LayerAlpha)
		}
	}
}

func (s *Services) d3dDeviceNative(vm *tjs.VM, context tjs.Value, className, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	id, err := d3dContextID(context, className+"."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	switch operation {
	case "@initialize":
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		s.removeD3DDevice(id)
		return tjs.VoidValue, nil
	case className:
		width, err := d3dIntArg(args, 0, className)
		if err != nil {
			return tjs.VoidValue, err
		}
		height, err := d3dIntArg(args, 1, className)
		if err != nil {
			return tjs.VoidValue, err
		}
		device, err := newD3DDeviceData(width, height)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		s.D3DDevices[id] = device
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "setSize":
		width, err := d3dIntArg(args, 0, className+".setSize")
		if err != nil {
			return tjs.VoidValue, err
		}
		height, err := d3dIntArg(args, 1, className+".setSize")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			if width < 0 || height < 0 || width > d3dMaxDimension || height > d3dMaxDimension ||
				uint64(width)*uint64(height) > d3dMaxPixels {
				s.mu.Unlock()
				return tjs.VoidValue, fmt.Errorf("%s.setSize: invalid dimensions %dx%d", className, width, height)
			}
			device.width, device.height = width, height
			device.primaryWidth, device.primaryHeight = width, height
			device.frame = nil
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.setSize: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "setPrimarySize":
		width, err := d3dIntArg(args, 0, className+".setPrimarySize")
		if err != nil {
			return tjs.VoidValue, err
		}
		height, err := d3dIntArg(args, 1, className+".setPrimarySize")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			if width < 0 || height < 0 || width > d3dMaxDimension || height > d3dMaxDimension ||
				uint64(width)*uint64(height) > d3dMaxPixels {
				s.mu.Unlock()
				return tjs.VoidValue, fmt.Errorf("%s.setPrimarySize: invalid dimensions %dx%d", className, width, height)
			}
			device.primaryWidth, device.primaryHeight = width, height
			device.frame = nil
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.setPrimarySize: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "setScreenRect":
		x, err := d3dIntArg(args, 0, className+".setScreenRect")
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := d3dIntArg(args, 1, className+".setScreenRect")
		if err != nil {
			return tjs.VoidValue, err
		}
		width, err := d3dIntArg(args, 2, className+".setScreenRect")
		if err != nil {
			return tjs.VoidValue, err
		}
		height, err := d3dIntArg(args, 3, className+".setScreenRect")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			device.screenX, device.screenY, device.screenWidth, device.screenHeight = x, y, width, height
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.setScreenRect: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "setOffset":
		x, err := d3dIntArg(args, 0, className+".setOffset")
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := d3dIntArg(args, 1, className+".setOffset")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			device.offsetX, device.offsetY = x, y
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.setOffset: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "update":
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			s.composeD3DDeviceFrameLocked(device)
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.update: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "capture":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("%s.capture: destination layer is required", className)
		}
		layerID, ok := d3dObjectID(args[0])
		if !ok {
			return tjs.VoidValue, fmt.Errorf("%s.capture: destination must be a Layer", className)
		}
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("%s.capture: device is unavailable", className)
		}
		s.composeD3DDeviceFrameLocked(device)
		width, height := device.frameWidth(), device.frameHeight()
		frame := append([]byte(nil), device.frame...)
		destination := s.Layers[layerID]
		s.mu.Unlock()
		if destination == nil {
			return tjs.VoidValue, fmt.Errorf("%s.capture: destination is not a Layer", className)
		}
		destination.ClearProvince()
		if err := destination.SetRGBA(width, height, frame); err != nil {
			return tjs.VoidValue, fmt.Errorf("%s.capture: %w", className, err)
		}
		if err := destination.SetSize(width, height); err != nil {
			return tjs.VoidValue, fmt.Errorf("%s.capture: %w", className, err)
		}
		return tjs.VoidValue, nil
	case "startTransition":
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			s.composeD3DDeviceFrameLocked(device)
			device.transitionSource = append(device.transitionSource[:0], device.frame...)
			device.inTransition = true
			device.transState = 1
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.startTransition: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "stopTransition":
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			device.inTransition = false
			device.transitionSource = nil
			device.transState = 0
			s.composeD3DDeviceFrameLocked(device)
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.stopTransition: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "checkEnable":
		return tjs.IntegerValue(0), nil
	case "getModule":
		return tjs.VoidValue, nil
	case "remove":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("%s.remove: missing object", className)
		}
		removeID, ok := d3dObjectID(args[0])
		if !ok {
			return tjs.VoidValue, nil
		}
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			for i, current := range device.layers {
				if current == removeID {
					copy(device.layers[i:], device.layers[i+1:])
					device.layers = device.layers[:len(device.layers)-1]
					break
				}
			}
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "get:width", "get:height":
		s.mu.RLock()
		device := s.D3DDevices[id]
		s.mu.RUnlock()
		if device == nil {
			return tjs.IntegerValue(0), nil
		}
		if operation == "get:width" {
			return tjs.IntegerValue(int64(device.width)), nil
		}
		return tjs.IntegerValue(int64(device.height)), nil
	case "get:clearColor":
		s.mu.RLock()
		device := s.D3DDevices[id]
		s.mu.RUnlock()
		if device == nil {
			return tjs.IntegerValue(0), nil
		}
		return tjs.IntegerValue(int64(device.clearColor)), nil
	case "set:clearColor":
		value, err := d3dUint32Arg(args, 0, className+".clearColor")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			device.clearColor = value
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.clearColor: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "get:transState":
		s.mu.RLock()
		device := s.D3DDevices[id]
		s.mu.RUnlock()
		if device == nil {
			return tjs.RealValue(0), nil
		}
		return tjs.RealValue(device.transState), nil
	case "set:transState":
		value, err := d3dFloatArg(args, 0, className+".transState")
		if err != nil {
			return tjs.VoidValue, err
		}
		if value < 0 {
			value = 0
		}
		if value > 1 {
			value = 1
		}
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			device.transState = value
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.transState: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "get:layerManagerIndex":
		s.mu.RLock()
		device := s.D3DDevices[id]
		s.mu.RUnlock()
		if device == nil {
			return tjs.IntegerValue(0), nil
		}
		return tjs.IntegerValue(int64(device.layerManagerIndex)), nil
	case "set:layerManagerIndex":
		value, err := d3dIntArg(args, 0, className+".layerManagerIndex")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			device.layerManagerIndex = value
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.layerManagerIndex: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "get:stretchType":
		s.mu.RLock()
		device := s.D3DDevices[id]
		s.mu.RUnlock()
		if device == nil {
			return tjs.IntegerValue(0), nil
		}
		return tjs.IntegerValue(int64(device.stretchType)), nil
	case "set:stretchType":
		value, err := d3dIntArg(args, 0, className+".stretchType")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			device.stretchType = value
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.stretchType: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "get:bicubicParam":
		s.mu.RLock()
		device := s.D3DDevices[id]
		s.mu.RUnlock()
		if device == nil {
			return tjs.RealValue(0), nil
		}
		return tjs.RealValue(device.bicubicParam), nil
	case "set:bicubicParam":
		value, err := d3dFloatArg(args, 0, className+".bicubicParam")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		device := s.D3DDevices[id]
		if device != nil {
			device.bicubicParam = value
		}
		s.mu.Unlock()
		if device == nil {
			return tjs.VoidValue, fmt.Errorf("%s.bicubicParam: device is unavailable", className)
		}
		return tjs.VoidValue, nil
	case "get:interface":
		return tjs.IntegerValue(int64(id)), nil
	case "get:primaryLayers", "get:children":
		s.mu.RLock()
		device := s.D3DDevices[id]
		layers := []tjs.ObjectID(nil)
		if device != nil {
			layers = append(layers, device.layers...)
		}
		s.mu.RUnlock()
		values := make([]tjs.Value, len(layers))
		for i, layerID := range layers {
			values[i] = tjs.ObjectIDValue(layerID)
		}
		return vm.NewArray(values)
	default:
		return tjs.VoidValue, fmt.Errorf("%w: %s.%s", ErrUnsupportedNative, className, operation)
	}
}

func installDrawDeviceD3DNative(vm *tjs.VM, services *Services) error {
	if vm == nil || services == nil {
		return fmt.Errorf("drawdeviceD3D.dll requires a VM and services")
	}
	if _, installed := vm.Globals["D3DImage"]; installed {
		return nil
	}
	image, err := vm.RegisterNativeClass("D3DImage")
	if err != nil {
		return err
	}
	for _, method := range []string{"D3DImage", "load"} {
		if err := vm.RegisterNativeMethod(image, method, "D3DImage."+method); err != nil {
			return err
		}
	}
	if err := vm.RegisterNativeMethod(image, "@invalidate", "D3DImage.@invalidate"); err != nil {
		return err
	}
	if err := vm.RegisterNativeMethod(image, "finalize", "D3DImage.finalize"); err != nil {
		return err
	}
	for _, property := range []string{"width", "height"} {
		if err := vm.RegisterNativeProperty(image, property, "D3DImage.get:"+property, ""); err != nil {
			return err
		}
	}

	picture, err := vm.RegisterNativeClass("D3DPicture")
	if err != nil {
		return err
	}
	for _, method := range []string{"D3DPicture", "assignImageRange", "clearImageRange", "setCoord", "draw"} {
		if err := vm.RegisterNativeMethod(picture, method, "D3DPicture."+method); err != nil {
			return err
		}
	}
	if err := vm.RegisterNativeMethod(picture, "@invalidate", "D3DPicture.@invalidate"); err != nil {
		return err
	}
	if err := vm.RegisterNativeMethod(picture, "finalize", "D3DPicture.finalize"); err != nil {
		return err
	}
	for _, property := range []string{"opacity", "blendMode"} {
		if err := vm.RegisterNativeProperty(picture, property, "D3DPicture.get:"+property, "D3DPicture.set:"+property); err != nil {
			return err
		}
	}

	layer, err := vm.RegisterNativeClass("D3DLayer")
	if err != nil {
		return err
	}
	layerClass, ok := vm.Objects.Lookup(layer.ObjectRef().Object)
	if !ok {
		return fmt.Errorf("D3DLayer native class is not registered")
	}
	for name, value := range map[string]int64{
		"DrawPlaneFront": int64(d3dDrawPlaneFront),
		"DrawPlaneBack":  int64(d3dDrawPlaneBack),
		"DrawPlaneBoth":  int64(d3dDrawPlaneBoth),
	} {
		if err := layerClass.Set(name, tjs.IntegerValue(value)); err != nil {
			return err
		}
	}
	for _, method := range []string{"D3DLayer", "setMatrix", "setClip", "draw", "update"} {
		if err := vm.RegisterNativeMethod(layer, method, "D3DLayer."+method); err != nil {
			return err
		}
	}
	if err := vm.RegisterNativeMethod(layer, "@invalidate", "D3DLayer.@invalidate"); err != nil {
		return err
	}
	if err := vm.RegisterNativeMethod(layer, "finalize", "D3DLayer.finalize"); err != nil {
		return err
	}
	for _, property := range []string{"drawPlane", "visible", "frontIndex", "backIndex", "opacity"} {
		if err := vm.RegisterNativeProperty(layer, property, "D3DLayer.get:"+property, "D3DLayer.set:"+property); err != nil {
			return err
		}
	}

	for _, className := range []string{"D3D", "DrawDeviceD3D"} {
		device, err := vm.RegisterNativeClass(className)
		if err != nil {
			return err
		}
		for _, method := range []string{className, "setSize", "setPrimarySize", "setScreenRect", "setOffset", "update", "capture", "startTransition", "stopTransition", "checkEnable", "getModule", "remove"} {
			if err := vm.RegisterNativeMethod(device, method, className+"."+method); err != nil {
				return err
			}
		}
		if err := vm.RegisterNativeMethod(device, "@invalidate", className+".@invalidate"); err != nil {
			return err
		}
		if err := vm.RegisterNativeMethod(device, "finalize", className+".finalize"); err != nil {
			return err
		}
		for _, property := range []string{"width", "height", "clearColor", "transState", "layerManagerIndex", "stretchType", "bicubicParam", "interface", "primaryLayers", "children"} {
			setter := className + ".set:" + property
			switch property {
			case "width", "height", "interface", "primaryLayers", "children":
				setter = ""
			}
			if err := vm.RegisterNativeProperty(device, property, className+".get:"+property, setter); err != nil {
				return err
			}
		}
	}
	return nil
}
