package assets

import (
	"bytes"
	"compress/zlib"
	"errors"
	"testing"
)

// psdTestChannel is one layer channel in a generated fixture.
type psdTestChannel struct {
	id          int16
	compression uint16
	plane       []byte
	width       int32
	height      int32
	// declaredLength overrides the channel length field so truncated-channel
	// fixtures can advertise more bytes than they carry.
	declaredLength *uint32
}

type psdTestMask struct {
	top, left, bottom, right int32
	defaultColor             byte
	flags                    byte
}

type psdTestLayer struct {
	top, left, bottom, right int32
	blend                    string
	opacity                  byte
	flags                    byte
	name                     string
	unicode                  string
	id                       int32
	section                  int32
	mask                     *psdTestMask
	channels                 []psdTestChannel
}

type psdTestDoc struct {
	width, height int32
	channels      uint16
	depth         uint16
	mode          uint16
	colorModeData []byte
	resources     []byte
	layers        []psdTestLayer
	composite     []psdTestComposite
}

type psdTestComposite struct {
	compression uint16
	planes      [][]byte
}

func psdTestBe16(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }
func psdTestBe32(v int32) []byte {
	return []byte{byte(uint32(v) >> 24), byte(uint32(v) >> 16), byte(uint32(v) >> 8), byte(v)}
}
func psdTestAppend32(dst []byte, v int32) []byte { return append(dst, psdTestBe32(v)...) }

func psdTestPackBitsRow(row []byte) []byte {
	var out []byte
	for len(row) > 0 {
		n := len(row)
		if n > 128 {
			n = 128
		}
		out = append(out, byte(n-1))
		out = append(out, row[:n]...)
		row = row[n:]
	}
	// A literal-only encoding is valid PackBits; keep an explicit terminator
	// out of the stream because the decoded row length is implied by width.
	return out
}

func psdTestEncodeChannel(t *testing.T, channel psdTestChannel) []byte {
	t.Helper()
	var body []byte
	switch channel.compression {
	case 0:
		body = channel.plane
	case 1:
		rowBytes := int(channel.width)
		var counts, rows []byte
		for y := 0; y < int(channel.height); y++ {
			encoded := psdTestPackBitsRow(channel.plane[y*rowBytes : (y+1)*rowBytes])
			counts = append(counts, psdTestBe16(uint16(len(encoded)))...)
			rows = append(rows, encoded...)
		}
		body = append(counts, rows...)
	case 2:
		var buffer bytes.Buffer
		writer := zlib.NewWriter(&buffer)
		if _, err := writer.Write(channel.plane); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		body = buffer.Bytes()
	case 3:
		predicted := append([]byte(nil), channel.plane...)
		for y := 0; y < int(channel.height); y++ {
			row := predicted[y*int(channel.width) : (y+1)*int(channel.width)]
			for x := len(row) - 1; x >= 1; x-- {
				row[x] -= row[x-1]
			}
		}
		var buffer bytes.Buffer
		writer := zlib.NewWriter(&buffer)
		if _, err := writer.Write(predicted); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		body = buffer.Bytes()
	default:
		t.Fatalf("unsupported fixture compression %d", channel.compression)
	}
	return append(psdTestBe16(channel.compression), body...)
}

func psdTestResource(id uint16, data []byte) []byte {
	out := append([]byte("8BIM"), psdTestBe16(id)...)
	out = append(out, 0, 0) // empty Pascal name plus its even-padding byte
	out = append(out, psdTestBe32(int32(len(data)))...)
	out = append(out, data...)
	if len(data)%2 != 0 {
		out = append(out, 0)
	}
	return out
}

func psdTestAdditional(key string, data []byte) []byte {
	out := append([]byte("8BIM"), []byte(key)...)
	out = append(out, psdTestBe32(int32(len(data)))...)
	return append(out, data...)
}

func psdTestLayerExtra(layer psdTestLayer) []byte {
	var extra []byte
	if layer.mask != nil {
		mask := append(psdTestBe32(layer.mask.top), psdTestBe32(layer.mask.left)...)
		mask = append(mask, psdTestBe32(layer.mask.bottom)...)
		mask = append(mask, psdTestBe32(layer.mask.right)...)
		mask = append(mask, layer.mask.defaultColor, layer.mask.flags, 0, 0)
		extra = append(extra, psdTestBe32(int32(len(mask)))...)
		extra = append(extra, mask...)
	} else {
		extra = append(extra, psdTestBe32(0)...) // layer mask size
	}
	extra = append(extra, psdTestBe32(0)...) // blending ranges size
	name := []byte(layer.name)
	extra = append(extra, byte(len(name)))
	extra = append(extra, name...)
	padding := (4 - (len(name)+1)&3) & 3
	extra = append(extra, make([]byte, padding)...)
	if layer.unicode != "" {
		units := []uint16{}
		for _, r := range layer.unicode {
			if r > 0xffff {
				r = 0xfffd
			}
			units = append(units, uint16(r))
		}
		data := psdTestBe32(int32(len(units)))
		for _, unit := range units {
			data = append(data, psdTestBe16(unit)...)
		}
		extra = append(extra, psdTestAdditional("luni", data)...)
	}
	if layer.id != 0 {
		extra = append(extra, psdTestAdditional("lyid", psdTestBe32(layer.id))...)
	}
	if layer.section != 0 {
		extra = append(extra, psdTestAdditional("lsct", psdTestBe32(layer.section))...)
	}
	return extra
}

func psdTestBuild(t *testing.T, doc psdTestDoc) []byte {
	t.Helper()
	if doc.channels == 0 {
		doc.channels = 3
	}
	if doc.depth == 0 {
		doc.depth = 8
	}
	if doc.mode == 0 {
		doc.mode = uint16(PSDColorModeRGB)
	}
	var out []byte
	out = append(out, []byte("8BPS")...)
	out = append(out, psdTestBe16(1)...)
	out = append(out, make([]byte, 6)...)
	out = append(out, psdTestBe16(doc.channels)...)
	out = append(out, psdTestBe32(doc.height)...)
	out = append(out, psdTestBe32(doc.width)...)
	out = append(out, psdTestBe16(doc.depth)...)
	out = append(out, psdTestBe16(doc.mode)...)
	out = append(out, psdTestBe32(int32(len(doc.colorModeData)))...)
	out = append(out, doc.colorModeData...)
	out = append(out, psdTestBe32(int32(len(doc.resources)))...)
	out = append(out, doc.resources...)

	var layerInfo []byte
	if len(doc.layers) > 0 {
		layerInfo = append(layerInfo, psdTestBe16(uint16(len(doc.layers)))...)
		type encodedLayer struct {
			record  []byte
			payload []byte
		}
		encoded := make([]encodedLayer, len(doc.layers))
		for i := range doc.layers {
			layer := doc.layers[i]
			var record []byte
			record = append(record, psdTestBe32(layer.top)...)
			record = append(record, psdTestBe32(layer.left)...)
			record = append(record, psdTestBe32(layer.bottom)...)
			record = append(record, psdTestBe32(layer.right)...)
			record = append(record, psdTestBe16(uint16(len(layer.channels)))...)
			payloads := make([][]byte, len(layer.channels))
			for j := range layer.channels {
				payloads[j] = psdTestEncodeChannel(t, layer.channels[j])
				length := uint32(len(payloads[j]))
				if layer.channels[j].declaredLength != nil {
					length = *layer.channels[j].declaredLength
				}
				record = append(record, psdTestBe16(uint16(layer.channels[j].id))...)
				record = append(record, psdTestBe32(int32(length))...)
			}
			blend := layer.blend
			if len(blend) != 4 {
				blend = "norm"
			}
			record = append(record, []byte("8BIM")...)
			record = append(record, []byte(blend)...)
			record = append(record, layer.opacity, 0, layer.flags, 0)
			extra := psdTestLayerExtra(layer)
			record = append(record, psdTestBe32(int32(len(extra)))...)
			record = append(record, extra...)
			encoded[i] = encodedLayer{record: record, payload: bytes.Join(payloads, nil)}
		}
		for i := range encoded {
			layerInfo = append(layerInfo, encoded[i].record...)
		}
		for i := range encoded {
			layerInfo = append(layerInfo, encoded[i].payload...)
		}
	}
	var layerAndMask []byte
	layerAndMask = append(layerAndMask, psdTestBe32(int32(len(layerInfo)))...)
	layerAndMask = append(layerAndMask, layerInfo...)
	layerAndMask = append(layerAndMask, psdTestBe32(0)...) // global layer mask info
	out = append(out, psdTestBe32(int32(len(layerAndMask)))...)
	out = append(out, layerAndMask...)

	if len(doc.composite) > 0 {
		comp := doc.composite[0]
		out = append(out, psdTestBe16(comp.compression)...)
		switch comp.compression {
		case 0:
			for _, plane := range comp.planes {
				out = append(out, plane...)
			}
		case 1:
			var counts, rows []byte
			height := int(doc.height)
			for _, plane := range comp.planes {
				for y := 0; y < height; y++ {
					encoded := psdTestPackBitsRow(plane[y*int(doc.width) : (y+1)*int(doc.width)])
					counts = append(counts, psdTestBe16(uint16(len(encoded)))...)
					rows = append(rows, encoded...)
				}
			}
			out = append(out, counts...)
			out = append(out, rows...)
		case 2, 3:
			for _, plane := range comp.planes {
				source := plane
				if comp.compression == 3 {
					source = append([]byte(nil), plane...)
					for y := 0; y < int(doc.height); y++ {
						row := source[y*int(doc.width) : (y+1)*int(doc.width)]
						for x := len(row) - 1; x >= 1; x-- {
							row[x] -= row[x-1]
						}
					}
				}
				var buffer bytes.Buffer
				writer := zlib.NewWriter(&buffer)
				if _, err := writer.Write(source); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				out = append(out, buffer.Bytes()...)
			}
		}
	}
	return out
}

func psdTestRGBPlanes() ([][]byte, []byte) {
	// 4x3 composite: a deterministic RGBA image.
	r := []byte{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120}
	g := []byte{200, 199, 198, 197, 196, 195, 194, 193, 192, 191, 190, 189}
	b := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	a := []byte{255, 254, 253, 252, 251, 250, 249, 248, 247, 246, 245, 244}
	return [][]byte{r, g, b, a}, r
}

func psdTestLayerChannels(red, green, blue, alpha []byte, compression uint16) []psdTestChannel {
	return []psdTestChannel{
		{id: 0, compression: compression, plane: red, width: 2, height: 2},
		{id: 1, compression: compression, plane: green, width: 2, height: 2},
		{id: 2, compression: compression, plane: blue, width: 2, height: 2},
		{id: -1, compression: compression, plane: alpha, width: 2, height: 2},
	}
}

func psdTestBaseDoc(composite psdTestComposite) psdTestDoc {
	return psdTestDoc{
		width:     4,
		height:    3,
		channels:  4,
		composite: []psdTestComposite{composite},
		layers: []psdTestLayer{{
			top: 1, left: 1, bottom: 3, right: 3,
			blend: "mul ", opacity: 128, name: "Layer One", unicode: "レイヤー", id: 7,
			channels: psdTestLayerChannels(
				[]byte{255, 0, 0, 255},
				[]byte{0, 255, 0, 255},
				[]byte{0, 0, 255, 128},
				[]byte{255, 128, 64, 32},
				0,
			),
		}},
	}
}

func TestParsePSDHeaderCompositeAndLayers(t *testing.T) {
	planes, _ := psdTestRGBPlanes()
	doc, err := ParsePSD(psdTestBuild(t, psdTestBaseDoc(psdTestComposite{compression: 0, planes: planes})))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Header.Version != 1 || doc.Header.Width != 4 || doc.Header.Height != 3 ||
		doc.Header.Channels != 4 || doc.Header.Depth != 8 || doc.Header.Mode != PSDColorModeRGB {
		t.Fatalf("header = %+v", doc.Header)
	}
	if len(doc.Layers) != 1 {
		t.Fatalf("layers = %d", len(doc.Layers))
	}
	layer := doc.Layers[0]
	if layer.Name != "レイヤー" || layer.LayerID != 7 || layer.Type != PSDLayerNormal ||
		layer.BlendMode != PSDBlendMultiply || layer.BlendModeKey != "mul " ||
		layer.Opacity != 128 || layer.FillOpacity != 255 || !layer.Visible() || layer.ParentIndex != -1 {
		t.Fatalf("layer = %+v", layer)
	}
	if layer.Top != 1 || layer.Left != 1 || layer.Width != 2 || layer.Height != 2 {
		t.Fatalf("layer rect = %+v", layer)
	}
	image, err := doc.Composite()
	if err != nil {
		t.Fatal(err)
	}
	if image.Width != 4 || image.Height != 3 {
		t.Fatalf("composite size = %dx%d", image.Width, image.Height)
	}
	for i, want := range []byte{10, 200, 0, 255, 20, 199, 1, 254} {
		if image.RGBA[i] != want {
			t.Fatalf("composite byte %d = %d, want %d", i, image.RGBA[i], want)
		}
	}
	layerImage, err := doc.LayerImage(0, PSDImageMasked)
	if err != nil {
		t.Fatal(err)
	}
	wantLayer := []byte{255, 0, 0, 255, 0, 255, 0, 128, 0, 0, 255, 64, 255, 255, 128, 32}
	if !bytes.Equal(layerImage.RGBA, wantLayer) {
		t.Fatalf("layer pixels = %v, want %v", layerImage.RGBA, wantLayer)
	}
	raw, err := doc.LayerImage(0, PSDImageRaw)
	if err != nil || !bytes.Equal(raw.RGBA, wantLayer) {
		t.Fatalf("raw layer = %v, %v", raw.RGBA, err)
	}
	mask, err := doc.LayerImage(0, PSDImageMask)
	if err != nil {
		t.Fatal(err)
	}
	if mask.Width != 1 || mask.Height != 1 || !bytes.Equal(mask.RGBA, []byte{0, 0, 0, 255}) {
		t.Fatalf("dummy mask = %dx%d %v", mask.Width, mask.Height, mask.RGBA)
	}
}

func TestPSDCompositeAndLayerRLE(t *testing.T) {
	planes, _ := psdTestRGBPlanes()
	doc := psdTestBaseDoc(psdTestComposite{compression: 1, planes: planes})
	doc.layers[0].channels = psdTestLayerChannels(
		[]byte{255, 0, 0, 255}, []byte{0, 255, 0, 255},
		[]byte{0, 0, 255, 128}, []byte{255, 128, 64, 32}, 1,
	)
	parsed, err := ParsePSD(psdTestBuild(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	image, err := parsed.Composite()
	if err != nil {
		t.Fatal(err)
	}
	if image.RGBA[0] != 10 || image.RGBA[1] != 200 || image.RGBA[3] != 255 || image.RGBA[47] != 244 {
		t.Fatalf("RLE composite pixels = %v", image.RGBA[:8])
	}
	layer, err := parsed.LayerImage(0, PSDImageMasked)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{255, 0, 0, 255, 0, 255, 0, 128, 0, 0, 255, 64, 255, 255, 128, 32}
	if !bytes.Equal(layer.RGBA, want) {
		t.Fatalf("RLE layer pixels = %v, want %v", layer.RGBA, want)
	}
}

func TestPSDLayerZIPAndPrediction(t *testing.T) {
	planes, _ := psdTestRGBPlanes()
	for _, compression := range []uint16{2, 3} {
		t.Run(map[uint16]string{2: "zip", 3: "zip-prediction"}[compression], func(t *testing.T) {
			doc := psdTestBaseDoc(psdTestComposite{compression: compression, planes: planes})
			doc.layers[0].channels = psdTestLayerChannels(
				[]byte{255, 0, 0, 255}, []byte{0, 255, 0, 255},
				[]byte{0, 0, 255, 128}, []byte{255, 128, 64, 32}, compression,
			)
			parsed, err := ParsePSD(psdTestBuild(t, doc))
			if err != nil {
				t.Fatal(err)
			}
			image, err := parsed.Composite()
			if err != nil {
				t.Fatal(err)
			}
			if image.RGBA[0] != 10 || image.RGBA[4] != 20 || image.RGBA[47] != 244 {
				t.Fatalf("ZIP composite pixels = %v", image.RGBA[:8])
			}
			layer, err := parsed.LayerImage(0, PSDImageRaw)
			if err != nil {
				t.Fatal(err)
			}
			want := []byte{255, 0, 0, 255, 0, 255, 0, 128, 0, 0, 255, 64, 255, 255, 128, 32}
			if !bytes.Equal(layer.RGBA, want) {
				t.Fatalf("ZIP layer pixels = %v, want %v", layer.RGBA, want)
			}
		})
	}
}

func TestPSDMaskMergesIntoAlpha(t *testing.T) {
	planes, _ := psdTestRGBPlanes()
	doc := psdTestBaseDoc(psdTestComposite{compression: 0, planes: planes})
	doc.layers[0].mask = &psdTestMask{top: 1, left: 1, bottom: 3, right: 3, defaultColor: 0xff}
	doc.layers[0].channels = append(doc.layers[0].channels, psdTestChannel{
		id: -2, compression: 0, width: 2, height: 2,
		plane: []byte{64, 128, 255, 255},
	})
	parsed, err := ParsePSD(psdTestBuild(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Layers[0].Mask.Width != 2 || parsed.Layers[0].Mask.Height != 2 {
		t.Fatalf("mask rect = %+v", parsed.Layers[0].Mask)
	}
	image, err := parsed.LayerImage(0, PSDImageMasked)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{255, 0, 0, 64, 0, 255, 0, 64, 0, 0, 255, 64, 255, 255, 128, 32}
	if !bytes.Equal(image.RGBA, want) {
		t.Fatalf("masked layer pixels = %v, want %v", image.RGBA, want)
	}
	raw, err := parsed.LayerImage(0, PSDImageRaw)
	if err != nil {
		t.Fatal(err)
	}
	if raw.RGBA[3] != 255 || raw.RGBA[15] != 32 {
		t.Fatalf("raw layer alpha = %d,%d", raw.RGBA[3], raw.RGBA[15])
	}
	mask, err := parsed.LayerImage(0, PSDImageMask)
	if err != nil {
		t.Fatal(err)
	}
	if mask.Width != 2 || mask.Height != 2 || mask.RGBA[0] != 64 || mask.RGBA[8] != 255 {
		t.Fatalf("mask image = %dx%d %v", mask.Width, mask.Height, mask.RGBA)
	}
}

func psdTestHeader(magic string, version, channels, depth, mode uint16, width, height int32) []byte {
	out := []byte(magic)
	out = append(out, psdTestBe16(version)...)
	out = append(out, make([]byte, 6)...)
	out = append(out, psdTestBe16(channels)...)
	out = append(out, psdTestBe32(height)...)
	out = append(out, psdTestBe32(width)...)
	out = append(out, psdTestBe16(depth)...)
	out = append(out, psdTestBe16(mode)...)
	return out
}

func TestPSDMalformedHeaders(t *testing.T) {
	cases := map[string][]byte{
		"empty":        nil,
		"short":        []byte("8BPS\x00\x01"),
		"signature":    psdTestHeader("XBPS", 1, 3, 8, 3, 4, 3),
		"version":      psdTestHeader("8BPS", 2, 3, 8, 3, 4, 3),
		"channels":     psdTestHeader("8BPS", 1, 0, 8, 3, 4, 3),
		"zero width":   psdTestHeader("8BPS", 1, 3, 8, 3, 0, 3),
		"bad depth":    psdTestHeader("8BPS", 1, 3, 3, 3, 4, 3),
		"unknown mode": psdTestHeader("8BPS", 1, 3, 8, 5, 4, 3),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePSD(data); !errors.Is(err, ErrPSD) {
				t.Fatalf("error = %v, want ErrPSD", err)
			}
		})
	}
}

func TestPSDTruncatedChannels(t *testing.T) {
	planes, _ := psdTestRGBPlanes()
	declared := uint32(9999)
	doc := psdTestBaseDoc(psdTestComposite{compression: 0, planes: planes})
	doc.layers[0].channels[0].declaredLength = &declared
	if _, err := ParsePSD(psdTestBuild(t, doc)); !errors.Is(err, ErrPSD) {
		t.Fatalf("declared channel error = %v, want ErrPSD", err)
	}

	// A channel whose declared length fits but whose RLE row counts exceed the
	// payload fails when the plane is decoded.
	doc = psdTestBaseDoc(psdTestComposite{compression: 0, planes: planes})
	doc.layers[0].channels = []psdTestChannel{{
		id: 0, compression: 1, width: 2, height: 2,
		plane: []byte{0, 0, 0, 0},
	}}
	parsed, err := ParsePSD(psdTestBuild(t, doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Replace the first channel payload with a row count larger than the data.
	channel := &parsed.Layers[0].Channels[0]
	channel.Payload = []byte{0, 1, 0xff, 0xff}
	if _, err := parsed.LayerImage(0, PSDImageRaw); !errors.Is(err, ErrPSD) {
		t.Fatalf("truncated RLE error = %v, want ErrPSD", err)
	}

	// Composite data cut inside the first channel plane.
	planes, _ = psdTestRGBPlanes()
	doc = psdTestBaseDoc(psdTestComposite{compression: 0, planes: planes})
	data := psdTestBuild(t, doc)
	headerAndSections := len(data) - 2 - 12*4
	parsed, err = ParsePSD(data[:headerAndSections+2+5])
	if err != nil {
		t.Fatalf("parse truncated composite: %v", err)
	}
	if _, err := parsed.Composite(); !errors.Is(err, ErrPSD) {
		t.Fatalf("truncated composite error = %v, want ErrPSD", err)
	}
}

func TestPSDUnsupportedModesAndDepth(t *testing.T) {
	planes, _ := psdTestRGBPlanes()
	doc := psdTestBaseDoc(psdTestComposite{compression: 0, planes: planes})
	doc.mode = uint16(PSDColorModeLAB)
	parsed, err := ParsePSD(psdTestBuild(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parsed.Composite(); !errors.Is(err, ErrPSDUnsupported) {
		t.Fatalf("Lab composite error = %v, want ErrPSDUnsupported", err)
	}
	if _, err := parsed.LayerImage(0, PSDImageRaw); !errors.Is(err, ErrPSDUnsupported) {
		t.Fatalf("Lab layer error = %v, want ErrPSDUnsupported", err)
	}

	doc = psdTestBaseDoc(psdTestComposite{compression: 0, planes: planes})
	doc.depth = 16
	parsed, err = ParsePSD(psdTestBuild(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parsed.Composite(); !errors.Is(err, ErrPSDUnsupported) {
		t.Fatalf("16-bit composite error = %v, want ErrPSDUnsupported", err)
	}
	if _, err := parsed.LayerImage(0, PSDImageRaw); !errors.Is(err, ErrPSDUnsupported) {
		t.Fatalf("16-bit layer error = %v, want ErrPSDUnsupported", err)
	}
}

func TestPSDResourceFlagsAndIndexedTable(t *testing.T) {
	table := make([]byte, 768)
	for i := 0; i < 256; i++ {
		table[i] = byte(i)
		table[256+i] = byte(255 - i)
		table[512+i] = byte(i / 2)
	}
	resource := append(psdTestResource(psdResourceSlices, []byte{0, 0, 0, 0}),
		psdTestResource(psdResourceGuides, []byte{0, 0, 0, 0})...)
	resource = append(resource, psdTestResource(psdResourceLayerComps, []byte{0, 0, 0, 16})...)
	doc := psdTestDoc{
		width: 2, height: 1, channels: 1, mode: uint16(PSDColorModeIndexed),
		colorModeData: table, resources: resource,
		composite: []psdTestComposite{{compression: 0, planes: [][]byte{{0, 1}}}},
	}
	parsed, err := ParsePSD(psdTestBuild(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.HasSlices || !parsed.HasGuides || !parsed.HasLayerComps {
		t.Fatalf("resource flags = slices:%v guides:%v comps:%v", parsed.HasSlices, parsed.HasGuides, parsed.HasLayerComps)
	}
	image, err := parsed.Composite()
	if err != nil {
		t.Fatal(err)
	}
	if image.RGBA[0] != 0 || image.RGBA[1] != 255 || image.RGBA[4] != 1 || image.RGBA[5] != 254 {
		t.Fatalf("indexed pixels = %v", image.RGBA)
	}
	if _, err := parsed.LayerImage(0, PSDImageRaw); err == nil {
		t.Fatal("expected missing-layer error")
	}
}

func TestPSDFolderParentsAndBlendModes(t *testing.T) {
	planes, _ := psdTestRGBPlanes()
	doc := psdTestBaseDoc(psdTestComposite{compression: 0, planes: planes})
	doc.layers = []psdTestLayer{
		{name: "end", section: 3, blend: "pass", flags: 2},
		{name: "child", top: 0, left: 0, bottom: 1, right: 1, blend: "scrn"},
		{name: "folder", section: 2, blend: "pass"},
	}
	doc.layers[1].channels = []psdTestChannel{
		{id: 0, compression: 0, plane: []byte{1}, width: 1, height: 1},
		{id: 1, compression: 0, plane: []byte{2}, width: 1, height: 1},
		{id: 2, compression: 0, plane: []byte{3}, width: 1, height: 1},
	}
	parsed, err := ParsePSD(psdTestBuild(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Layers) != 3 {
		t.Fatalf("layers = %d", len(parsed.Layers))
	}
	if parsed.Layers[0].Type != PSDLayerHidden || parsed.Layers[1].Type != PSDLayerNormal ||
		parsed.Layers[2].Type != PSDLayerFolder {
		t.Fatalf("types = %v %v %v", parsed.Layers[0].Type, parsed.Layers[1].Type, parsed.Layers[2].Type)
	}
	// The reference links the hidden end divider to the folder before popping
	// the folder from the parent stack.
	if parsed.Layers[1].ParentIndex != 2 || parsed.Layers[0].ParentIndex != 2 || parsed.Layers[2].ParentIndex != -1 {
		t.Fatalf("parents = %d %d %d", parsed.Layers[0].ParentIndex, parsed.Layers[1].ParentIndex, parsed.Layers[2].ParentIndex)
	}
	if parsed.Layers[1].BlendMode != PSDBlendScreen || parsed.Layers[2].BlendMode != PSDBlendPassThrough {
		t.Fatalf("blend modes = %v %v", parsed.Layers[1].BlendMode, parsed.Layers[2].BlendMode)
	}
	if parsed.Layers[0].Visible() || !parsed.Layers[1].Visible() {
		t.Fatalf("visibility = %v %v, want false true", parsed.Layers[0].Visible(), parsed.Layers[1].Visible())
	}
}

func TestPSDGrayscaleAlphaAndEmptyLayer(t *testing.T) {
	doc := psdTestDoc{
		width: 2, height: 1, channels: 2, mode: uint16(PSDColorModeGrayscale),
		composite: []psdTestComposite{{compression: 0, planes: [][]byte{{10, 200}, {255, 128}}}},
	}
	parsed, err := ParsePSD(psdTestBuild(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	image, err := parsed.Composite()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{10, 10, 10, 255, 200, 200, 200, 128}
	if !bytes.Equal(image.RGBA, want) {
		t.Fatalf("grayscale pixels = %v, want %v", image.RGBA, want)
	}

	empty := psdTestDoc{
		width: 2, height: 1, channels: 3,
		composite: []psdTestComposite{{compression: 0, planes: [][]byte{{0, 0}, {0, 0}, {0, 0}}}},
		layers: []psdTestLayer{{
			name: "empty", mask: &psdTestMask{defaultColor: 0xff},
		}},
	}
	parsed, err = ParsePSD(psdTestBuild(t, empty))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parsed.LayerImage(0, PSDImageRaw); !errors.Is(err, ErrPSDEmptyLayer) {
		t.Fatalf("empty layer error = %v, want ErrPSDEmptyLayer", err)
	}
	mask, err := parsed.LayerImage(0, PSDImageMask)
	if err != nil {
		t.Fatal(err)
	}
	if mask.Width != 1 || mask.Height != 1 || !bytes.Equal(mask.RGBA, []byte{0xff, 0xff, 0xff, 0xff}) {
		t.Fatalf("empty layer dummy mask = %dx%d %v", mask.Width, mask.Height, mask.RGBA)
	}
}
