package runtime

// The PSD plugin mirrors reference/krkr2-main/cpp/plugins/psdfile:
// psdclass.cpp exposes the PSD class, psdclass_loadstream.cpp reads the file
// through storage, and psdclass.cpp getBlend/getLayerData write the decoded
// planes into a Layer. The reference also registers a "psd://" storage media
// (main.cpp), exposing normal layers as BMP files through the storage APIs.

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type psdDocumentKey struct {
	vm *tjs.VM
	id tjs.ObjectID
}

var (
	psdInstallSequence    atomic.Uint64
	psdDocuments          sync.Map // psdDocumentKey -> *assets.PSD
	psdMediaRegistrations sync.Map // psdDocumentKey -> psdMediaRegistration
)

type psdMediaRegistration struct {
	storage  *assets.Storage
	document *assets.PSD
}

// installPSDNative registers the portable PSD class for psd.dll. The native
// operations carry a per-install suffix so two VMs sharing one Services
// registry do not overwrite each other's document closures.
func installPSDNative(vm *tjs.VM, services *Services) error {
	if vm == nil || services == nil {
		return fmt.Errorf("psd.dll requires a VM and services")
	}
	if _, installed := vm.Globals["PSD"]; installed {
		return nil
	}
	if services.Natives == nil {
		return fmt.Errorf("psd.dll requires a native registry")
	}
	prefix := fmt.Sprintf("PSD#%d.", psdInstallSequence.Add(1))
	native := func(operation string) NativeFunc {
		return func(context tjs.Value, args []tjs.Value) (tjs.Value, error) {
			return services.psdNative(vm, context, operation, args)
		}
	}
	register := func(operation string) error {
		return services.Natives.RegisterFunc(prefix+operation, native(operation))
	}
	for _, operation := range []string{
		"@initialize", "@invalidate", "PSD", "finalize", "load",
		"getLayerType", "getLayerName", "getLayerInfo",
		"getLayerData", "getLayerDataRaw", "getLayerDataMask",
		"getSlices", "getGuides", "getBlend", "getLayerComp",
		"clearStorageCache",
		"get:width", "get:height", "get:channels", "get:depth",
		"get:color_mode", "get:layer_count",
	} {
		if err := register(operation); err != nil {
			return err
		}
	}

	class, err := vm.RegisterNativeClass("PSD")
	if err != nil {
		return err
	}
	for _, method := range []string{
		"@initialize", "@invalidate", "PSD", "finalize", "load",
		"getLayerType", "getLayerName", "getLayerInfo",
		"getLayerData", "getLayerDataRaw", "getLayerDataMask",
		"getSlices", "getGuides", "getBlend", "getLayerComp",
	} {
		if err := vm.RegisterNativeMethod(class, method, prefix+method); err != nil {
			return err
		}
	}
	if err := vm.RegisterNativeStaticMethod(class, "clearStorageCache", prefix+"clearStorageCache"); err != nil {
		return err
	}
	for _, property := range []string{"width", "height", "channels", "depth", "color_mode", "layer_count"} {
		if err := vm.RegisterNativeProperty(class, property, prefix+"get:"+property, ""); err != nil {
			return err
		}
	}
	for name, value := range map[string]int64{
		"color_mode_bitmap":        int64(assets.PSDColorModeBitmap),
		"color_mode_grayscale":     int64(assets.PSDColorModeGrayscale),
		"color_mode_indexed":       int64(assets.PSDColorModeIndexed),
		"color_mode_rgb":           int64(assets.PSDColorModeRGB),
		"color_mode_cmyk":          int64(assets.PSDColorModeCMYK),
		"color_mode_multichannel":  int64(assets.PSDColorModeMultichannel),
		"color_mode_duotone":       int64(assets.PSDColorModeDuotone),
		"color_mode_lab":           int64(assets.PSDColorModeLAB),
		"blend_mode_normal":        int64(assets.PSDBlendNormal),
		"blend_mode_dissolve":      int64(assets.PSDBlendDissolve),
		"blend_mode_darken":        int64(assets.PSDBlendDarken),
		"blend_mode_multiply":      int64(assets.PSDBlendMultiply),
		"blend_mode_color_burn":    int64(assets.PSDBlendColorBurn),
		"blend_mode_linear_burn":   int64(assets.PSDBlendLinearBurn),
		"blend_mode_lighten":       int64(assets.PSDBlendLighten),
		"blend_mode_screen":        int64(assets.PSDBlendScreen),
		"blend_mode_color_dodge":   int64(assets.PSDBlendColorDodge),
		"blend_mode_linear_dodge":  int64(assets.PSDBlendLinearDodge),
		"blend_mode_overlay":       int64(assets.PSDBlendOverlay),
		"blend_mode_soft_light":    int64(assets.PSDBlendSoftLight),
		"blend_mode_hard_light":    int64(assets.PSDBlendHardLight),
		"blend_mode_vivid_light":   int64(assets.PSDBlendVividLight),
		"blend_mode_linear_light":  int64(assets.PSDBlendLinearLight),
		"blend_mode_pin_light":     int64(assets.PSDBlendPinLight),
		"blend_mode_hard_mix":      int64(assets.PSDBlendHardMix),
		"blend_mode_difference":    int64(assets.PSDBlendDifference),
		"blend_mode_exclusion":     int64(assets.PSDBlendExclusion),
		"blend_mode_hue":           int64(assets.PSDBlendHue),
		"blend_mode_saturation":    int64(assets.PSDBlendSaturation),
		"blend_mode_color":         int64(assets.PSDBlendColor),
		"blend_mode_luminosity":    int64(assets.PSDBlendLuminosity),
		"blend_mode_pass_through":  int64(assets.PSDBlendPassThrough),
		"blend_mode_darker_color":  int64(assets.PSDBlendDarkerColor),
		"blend_mode_lighter_color": int64(assets.PSDBlendLighterColor),
		"blend_mode_subtract":      int64(assets.PSDBlendSubtract),
		"blend_mode_divide":        int64(assets.PSDBlendDivide),
		"layer_type_normal":        int64(assets.PSDLayerNormal),
		"layer_type_hidden":        int64(assets.PSDLayerHidden),
		"layer_type_folder":        int64(assets.PSDLayerFolder),
		"layer_type_adjust":        int64(assets.PSDLayerAdjust),
		"layer_type_fill":          int64(assets.PSDLayerFill),
	} {
		if err := vm.RegisterNativeStaticValue(class, name, tjs.IntegerValue(value)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Services) psdNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	if vm == nil {
		return tjs.VoidValue, fmt.Errorf("PSD.%s requires a VM", operation)
	}
	if context.Kind() != tjs.KindObject || context.IsNull() {
		return tjs.VoidValue, fmt.Errorf("PSD.%s requires an instance", operation)
	}
	key := psdDocumentKey{vm: vm, id: context.ObjectRef().Object}
	document := func() (*assets.PSD, error) {
		value, ok := psdDocuments.Load(key)
		if !ok {
			return nil, fmt.Errorf("PSD.%s: no data", operation)
		}
		return value.(*assets.PSD), nil
	}
	switch operation {
	case "@initialize", "PSD":
		return tjs.VoidValue, nil
	case "@invalidate", "finalize":
		removePSDDocument(key)
		return tjs.VoidValue, nil
	case "load":
		removePSDDocument(key)
		if len(args) == 0 {
			return tjs.IntegerValue(0), nil
		}
		if args[0].Kind() != tjs.KindString {
			return tjs.VoidValue, fmt.Errorf("PSD.load requires a storage name")
		}
		data, err := s.readPSDData(args[0].Text())
		if err != nil {
			return tjs.IntegerValue(0), nil
		}
		doc, err := assets.ParsePSD(data)
		if err != nil {
			return tjs.IntegerValue(0), nil
		}
		if s.Storage != nil {
			if err := s.Storage.RegisterPSDMedia(args[0].Text(), doc); err != nil {
				return tjs.VoidValue, err
			}
			psdMediaRegistrations.Store(key, psdMediaRegistration{storage: s.Storage, document: doc})
		}
		psdDocuments.Store(key, doc)
		return tjs.IntegerValue(1), nil
	case "get:width":
		doc, err := document()
		if err != nil {
			return tjs.IntegerValue(-1), nil
		}
		return tjs.IntegerValue(int64(doc.Header.Width)), nil
	case "get:height":
		doc, err := document()
		if err != nil {
			return tjs.IntegerValue(-1), nil
		}
		return tjs.IntegerValue(int64(doc.Header.Height)), nil
	case "get:channels":
		doc, err := document()
		if err != nil {
			return tjs.IntegerValue(-1), nil
		}
		return tjs.IntegerValue(int64(doc.Header.Channels)), nil
	case "get:depth":
		doc, err := document()
		if err != nil {
			return tjs.IntegerValue(-1), nil
		}
		return tjs.IntegerValue(int64(doc.Header.Depth)), nil
	case "get:color_mode":
		doc, err := document()
		if err != nil {
			return tjs.IntegerValue(-1), nil
		}
		return tjs.IntegerValue(int64(doc.Header.Mode)), nil
	case "get:layer_count":
		doc, err := document()
		if err != nil {
			return tjs.IntegerValue(-1), nil
		}
		return tjs.IntegerValue(int64(len(doc.Layers))), nil
	case "getLayerType":
		doc, err := document()
		if err != nil {
			return tjs.VoidValue, err
		}
		index, err := psdLayerIndex(doc, operation, args)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.IntegerValue(int64(doc.Layers[index].Type)), nil
	case "getLayerName":
		doc, err := document()
		if err != nil {
			return tjs.VoidValue, err
		}
		index, err := psdLayerIndex(doc, operation, args)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.StringValue(doc.Layers[index].Name), nil
	case "getLayerInfo":
		doc, err := document()
		if err != nil {
			return tjs.VoidValue, err
		}
		index, err := psdLayerIndex(doc, operation, args)
		if err != nil {
			return tjs.VoidValue, err
		}
		return psdLayerInfo(vm, doc, index)
	case "getLayerData":
		return s.psdLayerData(vm, key, operation, args, assets.PSDImageMasked)
	case "getLayerDataRaw":
		return s.psdLayerData(vm, key, operation, args, assets.PSDImageRaw)
	case "getLayerDataMask":
		return s.psdLayerData(vm, key, operation, args, assets.PSDImageMask)
	case "getBlend":
		return s.psdBlend(key, operation, args)
	case "getLayerComp":
		doc, err := document()
		if err != nil {
			return tjs.VoidValue, err
		}
		if doc.HasLayerComps {
			return tjs.VoidValue, fmt.Errorf("%w: layer comps", assets.ErrPSDUnsupported)
		}
		return tjs.VoidValue, nil
	case "getSlices":
		doc, err := document()
		if err != nil {
			return tjs.VoidValue, err
		}
		if doc.HasSlices {
			return tjs.VoidValue, fmt.Errorf("%w: slices", assets.ErrPSDUnsupported)
		}
		return tjs.VoidValue, nil
	case "getGuides":
		doc, err := document()
		if err != nil {
			return tjs.VoidValue, err
		}
		if doc.HasGuides {
			return tjs.VoidValue, fmt.Errorf("%w: guides", assets.ErrPSDUnsupported)
		}
		return tjs.VoidValue, nil
	case "clearStorageCache":
		if s.Storage != nil {
			s.Storage.ClearPSDMediaCache()
		}
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("unsupported PSD operation %q", operation)
	}
}

func psdLayerIndex(doc *assets.PSD, operation string, args []tjs.Value) (int, error) {
	if len(args) == 0 {
		return 0, fmt.Errorf("PSD.%s: missing layer number", operation)
	}
	number, err := args[0].Int()
	if err != nil {
		return 0, fmt.Errorf("PSD.%s: layer number is not an integer", operation)
	}
	if number < 0 || number >= int64(len(doc.Layers)) {
		return 0, fmt.Errorf("PSD.%s: not such layer", operation)
	}
	return int(number), nil
}

func psdLayerInfo(vm *tjs.VM, doc *assets.PSD, index int) (tjs.Value, error) {
	layer := &doc.Layers[index]
	dictionary, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	set := func(name string, value tjs.Value) error {
		return tjs.SetMember(vm.Objects, dictionary, name, value)
	}
	mask := false
	for i := range layer.Channels {
		if layer.Channels[i].IsMask() {
			mask = true
			break
		}
	}
	values := map[string]tjs.Value{
		"top":                    tjs.IntegerValue(int64(layer.Top)),
		"left":                   tjs.IntegerValue(int64(layer.Left)),
		"bottom":                 tjs.IntegerValue(int64(layer.Bottom)),
		"right":                  tjs.IntegerValue(int64(layer.Right)),
		"width":                  tjs.IntegerValue(int64(layer.Width)),
		"height":                 tjs.IntegerValue(int64(layer.Height)),
		"opacity":                tjs.IntegerValue(int64(layer.Opacity)),
		"fill_opacity":           tjs.IntegerValue(int64(layer.FillOpacity)),
		"mask":                   tjs.IntegerValue(psdBoolInt(mask)),
		"type":                   tjs.IntegerValue(int64(psdLayerBlendType(layer.BlendMode))),
		"layer_type":             tjs.IntegerValue(int64(layer.Type)),
		"blend_mode":             tjs.IntegerValue(int64(layer.BlendMode)),
		"visible":                tjs.IntegerValue(psdBoolInt(layer.Visible())),
		"name":                   tjs.StringValue(layer.Name),
		"clipping":               tjs.IntegerValue(int64(layer.Clipping)),
		"layer_id":               tjs.IntegerValue(int64(layer.LayerID)),
		"obsolete":               tjs.IntegerValue(psdBoolInt(layer.Obsolete())),
		"transparency_protected": tjs.IntegerValue(psdBoolInt(layer.TransparencyProtected())),
		"pixel_data_irrelevant":  tjs.IntegerValue(psdBoolInt(layer.PixelDataIrrelevant())),
	}
	for name, value := range values {
		if err := set(name, value); err != nil {
			return tjs.VoidValue, err
		}
	}
	if layer.ParentIndex >= 0 && layer.ParentIndex < len(doc.Layers) {
		if err := set("group_layer_id", tjs.IntegerValue(int64(doc.Layers[layer.ParentIndex].LayerID))); err != nil {
			return tjs.VoidValue, err
		}
	}
	return dictionary, nil
}

func psdBoolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// psdLayerBlendType converts psd::BlendMode to the Layer type constants
// exposed by the runtime (ltPs*), matching convBlendMode in psdclass.cpp.
func psdLayerBlendType(mode assets.PSDBlendMode) LayerType {
	switch mode {
	case assets.PSDBlendNormal:
		return LayerPSNormal
	case assets.PSDBlendDarken:
		return LayerPSDarken
	case assets.PSDBlendMultiply:
		return LayerPSMultiplicative
	case assets.PSDBlendColorBurn:
		return LayerPSColorBurn
	case assets.PSDBlendLinearBurn:
		return LayerPSSubtractive
	case assets.PSDBlendLighten:
		return LayerPSLighten
	case assets.PSDBlendScreen:
		return LayerPSScreen
	case assets.PSDBlendColorDodge:
		return LayerPSColorDodge
	case assets.PSDBlendLinearDodge:
		return LayerPSAdditive
	case assets.PSDBlendOverlay:
		return LayerPSOverlay
	case assets.PSDBlendSoftLight:
		return LayerPSSoftLight
	case assets.PSDBlendHardLight:
		return LayerPSHardLight
	case assets.PSDBlendDifference:
		return LayerPSDifference
	case assets.PSDBlendExclusion:
		return LayerPSExclusion
	default:
		return LayerPSNormal
	}
}

func (s *Services) psdLayerData(vm *tjs.VM, key psdDocumentKey, operation string, args []tjs.Value, mode assets.PSDImageMode) (tjs.Value, error) {
	value, ok := psdDocuments.Load(key)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("PSD.%s: no data", operation)
	}
	doc := value.(*assets.PSD)
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("PSD.%s requires a Layer and a layer number", operation)
	}
	index, err := psdLayerIndex(doc, operation, args[1:])
	if err != nil {
		return tjs.VoidValue, err
	}
	layer := &doc.Layers[index]
	if layer.Type != assets.PSDLayerNormal && !(layer.Type == assets.PSDLayerFolder && mode == assets.PSDImageMask) {
		return tjs.VoidValue, fmt.Errorf("PSD.%s: invalid layer type", operation)
	}
	if args[0].Kind() != tjs.KindObject || args[0].IsNull() {
		return tjs.VoidValue, fmt.Errorf("PSD.%s: not layer", operation)
	}
	id := args[0].ObjectRef().Object
	s.mu.RLock()
	target := s.Layers[id]
	s.mu.RUnlock()
	if target == nil {
		return tjs.VoidValue, fmt.Errorf("PSD.%s: not layer", operation)
	}
	if mode != assets.PSDImageMask && (layer.Width <= 0 || layer.Height <= 0) {
		// psdclass.cpp returns without touching the Layer for zero-sized layers.
		return tjs.VoidValue, nil
	}
	image, err := doc.LayerImage(index, mode)
	if errors.Is(err, assets.ErrPSDEmptyLayer) {
		return tjs.VoidValue, nil
	}
	if err != nil {
		return tjs.VoidValue, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if target = s.Layers[id]; target == nil {
		return tjs.VoidValue, fmt.Errorf("PSD.%s: not layer", operation)
	}
	left, top := layer.Left, layer.Top
	opacity := int(layer.Opacity)
	layerType := psdLayerBlendType(layer.BlendMode)
	if mode == assets.PSDImageMask {
		mask := layer.Mask
		left, top = mask.Left, mask.Top
		if mask.Width <= 0 || mask.Height <= 0 {
			// psdclass.cpp replaces an absent mask with a 1x1 pixel at 0,0.
			left, top = 0, 0
		}
		opacity = 255
		layerType = LayerPSNormal
	}
	if err := target.SetBounds(Rect{X: left, Y: top, Width: int32(image.Width), Height: int32(image.Height)}); err != nil {
		return tjs.VoidValue, err
	}
	target.ImageLeft, target.ImageTop = 0, 0
	if err := target.SetRGBA(int32(image.Width), int32(image.Height), image.RGBA); err != nil {
		return tjs.VoidValue, err
	}
	if err := target.SetOpacity(opacity); err != nil {
		return tjs.VoidValue, err
	}
	target.Type = layerType
	target.Visible = layer.Visible()
	target.Name = layer.Name
	return tjs.VoidValue, nil
}

func (s *Services) psdBlend(key psdDocumentKey, operation string, args []tjs.Value) (tjs.Value, error) {
	value, ok := psdDocuments.Load(key)
	if !ok {
		return tjs.IntegerValue(0), nil
	}
	doc := value.(*assets.PSD)
	if len(args) == 0 {
		return tjs.VoidValue, fmt.Errorf("PSD.%s requires a Layer", operation)
	}
	if args[0].Kind() != tjs.KindObject || args[0].IsNull() {
		return tjs.VoidValue, fmt.Errorf("PSD.%s: not layer", operation)
	}
	id := args[0].ObjectRef().Object
	image, err := doc.Composite()
	if errors.Is(err, assets.ErrPSDNoComposite) {
		return tjs.IntegerValue(0), nil
	}
	if err != nil {
		return tjs.VoidValue, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target := s.Layers[id]
	if target == nil {
		return tjs.VoidValue, fmt.Errorf("PSD.%s: not layer", operation)
	}
	width, height := int32(image.Width), int32(image.Height)
	if err := target.SetBounds(Rect{X: target.Bounds.X, Y: target.Bounds.Y, Width: width, Height: height}); err != nil {
		return tjs.VoidValue, err
	}
	target.ImageLeft, target.ImageTop = 0, 0
	if err := target.SetRGBA(width, height, image.RGBA); err != nil {
		return tjs.VoidValue, err
	}
	return tjs.IntegerValue(1), nil
}

// clearPSDDocuments releases documents owned by a closing VM. Native
// finalizers usually do this during collection, but Session.Close drops the VM
// without a collection cycle.
func clearPSDDocuments(vm *tjs.VM) {
	if vm == nil {
		return
	}
	psdDocuments.Range(func(key, _ any) bool {
		if key.(psdDocumentKey).vm == vm {
			removePSDDocument(key.(psdDocumentKey))
		}
		return true
	})
}

func removePSDDocument(key psdDocumentKey) {
	psdDocuments.Delete(key)
	if value, ok := psdMediaRegistrations.LoadAndDelete(key); ok {
		registration := value.(psdMediaRegistration)
		registration.storage.UnregisterPSDMedia(registration.document)
	}
}

func (s *Services) readPSDData(name string) ([]byte, error) {
	if name == "" {
		return nil, errors.New("empty PSD storage name")
	}
	var storageErr error
	if s != nil && (s.Storage != nil || s.SaveDir != "") {
		data, err := s.readStorage(name)
		if err == nil {
			return data, nil
		}
		storageErr = err
	}
	data, err := os.ReadFile(name)
	if err == nil {
		return data, nil
	}
	if storageErr != nil {
		return nil, storageErr
	}
	return nil, err
}
