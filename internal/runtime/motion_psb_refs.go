package runtime

import (
	"strconv"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

func cloneMotionDocument(source *assets.Document) *assets.Document {
	if source == nil {
		return nil
	}
	var clone func(assets.Value) assets.Value
	clone = func(value assets.Value) assets.Value {
		switch node := value.(type) {
		case []assets.Value:
			result := make([]assets.Value, len(node))
			for index, child := range node {
				result[index] = clone(child)
			}
			return result
		case map[string]assets.Value:
			result := make(map[string]assets.Value, len(node))
			for key, child := range node {
				result[key] = clone(child)
			}
			return result
		default:
			return value
		}
	}
	root := clone(source.Root)
	result := &assets.Document{Version: source.Version, Root: root, Objects: make([]assets.PSBObject, 0, len(source.Objects))}
	for _, object := range source.Objects {
		value := root
		for _, part := range object.Path {
			switch node := value.(type) {
			case map[string]assets.Value:
				value = node[part]
			case []assets.Value:
				index, err := strconv.Atoi(part)
				if err != nil || index < 0 || index >= len(node) {
					value = nil
					break
				}
				value = node[index]
			default:
				value = nil
			}
		}
		if dictionary, ok := value.(map[string]assets.Value); ok {
			result.Objects = append(result.Objects, assets.PSBObject{Path: append([]string(nil), object.Path...), Value: dictionary})
		}
	}
	return result
}

func motionModuleDocument(resource *motionResource) *assets.Document {
	if resource == nil {
		return nil
	}
	if resource.moduleDoc != nil {
		return resource.moduleDoc
	}
	return resource.doc
}

// resolveMotionPSBReferences mirrors RuntimeSupport.cpp's ordered snapshot
// scan and resolveLayerDictionaryReference. Node and frame arrays may contain
// either inline dictionaries or integer indices into the snapshot layer list.
func resolveMotionPSBReferences(doc *assets.Document) {
	if doc == nil {
		return
	}
	layers := motionSnapshotLayerList(doc)
	for _, object := range doc.Objects {
		for _, key := range []string{"children", "frameList"} {
			items, ok := object.Value[key].([]assets.Value)
			if !ok {
				continue
			}
			for index, item := range items {
				layerIndex, ok := psbNumber(item)
				if !ok {
					continue
				}
				resolvedIndex := int(layerIndex)
				if resolvedIndex >= 0 && resolvedIndex < len(layers) {
					if _, valid := layers[resolvedIndex].(map[string]assets.Value); valid {
						items[index] = layers[resolvedIndex]
					}
				}
			}
		}
	}
}

func motionSnapshotLayerList(doc *assets.Document) []assets.Value {
	if doc == nil {
		return nil
	}
	layers := make([]assets.Value, 0)
	for _, object := range doc.Objects {
		if motionPSBObjectIsLayer(object) {
			layers = append(layers, object.Value)
		}
	}
	return layers
}

func motionResolveLayerReferences(items []assets.Value, layerList []assets.Value) []assets.Value {
	resolved := make([]assets.Value, 0, len(items))
	for _, item := range items {
		if _, inline := item.(map[string]assets.Value); inline {
			resolved = append(resolved, item)
			continue
		}
		if layerIndex, ok := psbNumber(item); ok {
			index := int(layerIndex)
			if index >= 0 && index < len(layerList) {
				if _, valid := layerList[index].(map[string]assets.Value); valid {
					resolved = append(resolved, layerList[index])
				}
			}
		}
	}
	return resolved
}

func motionPSBObjectIsLayer(object assets.PSBObject) bool {
	pathMatch := false
	for _, part := range object.Path {
		if strings.Contains(strings.ToLower(part), "layer") {
			pathMatch = true
			break
		}
	}
	_, hasLabel := motionPSBString(object.Value, "label", "name", "id")
	_, hasFrameList := object.Value["frameList"]
	_, hasLayerID := object.Value["layer_id"]
	_, hasLayerType := object.Value["layer_type"]
	_, hasWidth := object.Value["width"]
	_, hasHeight := object.Value["height"]
	_, hasLeft := object.Value["left"]
	_, hasTop := object.Value["top"]
	if !hasLabel {
		return false
	}
	return pathMatch || hasLayerID || hasLayerType || hasFrameList ||
		hasWidth && hasHeight && (hasLeft || hasTop)
}

func motionPSBString(value map[string]assets.Value, keys ...string) (string, bool) {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && text != "" {
			return text, true
		}
	}
	return "", false
}
