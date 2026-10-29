package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const (
	layerExBrush = iota
	layerExPen
)

const (
	layerExSolid = iota
	layerExHatch
	layerExTexture
	layerExPathGradient
	layerExLinearGradient
)

type layerExPoint struct{ x, y float64 }

type layerExRect struct{ x, y, w, h float64 }

type layerExAffine struct {
	a, b, c, d, tx, ty float64
}
type layerExMatrixState struct {
	matrix layerExAffine
	status int64
}

func layerExMatrixInvalidOrder(order int64) bool {
	return order != 0 && order != 1
}

func layerExF32(value float64) float64 {
	return float64(float32(value))
}

func layerExAffineValue(a, b, c, d, tx, ty float64) layerExAffine {
	return layerExAffine{
		a:  layerExF32(a),
		b:  layerExF32(b),
		c:  layerExF32(c),
		d:  layerExF32(d),
		tx: layerExF32(tx),
		ty: layerExF32(ty),
	}
}

func layerExIdentity() layerExAffine { return layerExAffineValue(1, 0, 0, 1, 0, 0) }

func (m layerExAffine) apply(p layerExPoint) layerExPoint {
	x, y := float32(p.x), float32(p.y)
	return layerExPoint{
		float64(float32(m.a)*x + float32(m.c)*y + float32(m.tx)),
		float64(float32(m.b)*x + float32(m.d)*y + float32(m.ty)),
	}
}

func (m layerExAffine) multiply(n layerExAffine) layerExAffine {
	mm := [6]float32{float32(m.a), float32(m.b), float32(m.c), float32(m.d), float32(m.tx), float32(m.ty)}
	nn := [6]float32{float32(n.a), float32(n.b), float32(n.c), float32(n.d), float32(n.tx), float32(n.ty)}
	return layerExAffineValue(
		float64(mm[0]*nn[0]+mm[2]*nn[1]),
		float64(mm[1]*nn[0]+mm[3]*nn[1]),
		float64(mm[0]*nn[2]+mm[2]*nn[3]),
		float64(mm[1]*nn[2]+mm[3]*nn[3]),
		float64(mm[0]*nn[4]+mm[2]*nn[5]+mm[4]),
		float64(mm[1]*nn[4]+mm[3]*nn[5]+mm[5]),
	)
}

func (m layerExAffine) invert() (layerExAffine, bool) {
	a, b, c, d := float32(m.a), float32(m.b), float32(m.c), float32(m.d)
	tx, ty := float32(m.tx), float32(m.ty)
	det := a*d - b*c
	if det == 0 || math.IsNaN(float64(det)) || math.IsInf(float64(det), 0) {
		return layerExAffine{}, false
	}
	inv := float32(1) / det
	return layerExAffineValue(
		float64(d*inv),
		float64(-b*inv),
		float64(-c*inv),
		float64(a*inv),
		float64((c*ty-d*tx)*inv),
		float64((b*tx-a*ty)*inv),
	), true
}

func layerExTranslation(x, y float64) layerExAffine {
	return layerExAffineValue(1, 0, 0, 1, x, y)
}

func layerExScale(x, y float64) layerExAffine {
	return layerExAffineValue(x, 0, 0, y, 0, 0)
}

func layerExRotation(degrees float64) layerExAffine {
	rad := float32(float32(degrees) * float32(math.Pi) / 180)
	c := float32(math.Cos(float64(rad)))
	s := float32(math.Sin(float64(rad)))
	return layerExAffineValue(float64(c), float64(s), -float64(s), float64(c), 0, 0)
}

type layerExPaint struct {
	kind           int
	brushType      int
	color          uint32
	color2         uint32
	gradientColors []uint32
	gradientStops  []float64
	p1, p2         layerExPoint
	center         layerExPoint
	radius         float64
	offset         layerExPoint
	rect           layerExRect
	hatch          int
	width          float64
	dashStyle      int
	dash           []float64
	dashOffset     float64
	lineCap        int
	endCap         int
	lineJoin       int
	texture        *assets.Image
	wrapMode       int
	textureRect    layerExRect
	hasTextureRect bool
}

type layerExAppearance struct{ paints []layerExPaint }

type layerExSubpath struct {
	points []layerExPoint
	closed bool
}

type layerExPath struct {
	subpaths []layerExSubpath
}

type layerExImage struct {
	image assets.Image
	valid bool
}

type layerExFont struct {
	familyName      string
	emSize          float64
	style           int64
	forceSelfPath   bool
	metricsRevision uint64
}

type layerExDrawState struct {
	updateWhenDraw    bool
	smoothingMode     int64
	textRenderingHint int64
	record            bool
	transform         layerExAffine
	viewTransform     layerExAffine
	recordPixels      []byte
	recordWidth       int32
	recordHeight      int32
}

type layerExObjectState struct {
	kind  string
	value any
}

type layerExRecordHeader struct {
	Magic  string `json:"magic"`
	Width  int32  `json:"width"`
	Height int32  `json:"height"`
}

func layerExObjectID(value tjs.Value, operation string) (tjs.ObjectID, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() || value.ObjectRef().Object == 0 {
		return 0, fmt.Errorf("%s requires a non-null object context", operation)
	}
	return value.ObjectRef().Object, nil
}

func layerExFloat(value tjs.Value, operation string) (float64, error) {
	n, err := value.Real()
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		if err != nil {
			return 0, fmt.Errorf("%s requires a finite number: %w", operation, err)
		}
		return 0, fmt.Errorf("%s requires a finite number", operation)
	}
	return n, nil
}

func layerExInt(value tjs.Value, operation string) (int64, error) {
	n, err := value.Int()
	if err != nil {
		return 0, fmt.Errorf("%s requires an integer: %w", operation, err)
	}
	return n, nil
}

func layerExBool(value tjs.Value, operation string) (bool, error) {
	v, err := value.Truth()
	if err != nil {
		return false, fmt.Errorf("%s requires a boolean: %w", operation, err)
	}
	return v, nil
}

func layerExMember(vm *tjs.VM, host *Services, value tjs.Value, name string, budget *uint64) (tjs.Value, bool, error) {
	if vm == nil || value.Kind() != tjs.KindObject || value.IsNull() {
		return tjs.VoidValue, false, nil
	}
	return vm.GetMemberIfPresent(value, name, host, budget)
}

func layerExIntMember(vm *tjs.VM, host *Services, value tjs.Value, name string, fallback int64, budget *uint64) int64 {
	member, found, err := layerExMember(vm, host, value, name, budget)
	if err != nil || !found {
		return fallback
	}
	n, err := member.Int()
	if err != nil {
		return fallback
	}
	return n
}

func layerExFloatMember(vm *tjs.VM, host *Services, value tjs.Value, name string, fallback float64, budget *uint64) float64 {
	member, found, err := layerExMember(vm, host, value, name, budget)
	if err != nil || !found {
		return fallback
	}
	n, err := member.Real()
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return fallback
	}
	return n
}

func layerExPointFromValue(vm *tjs.VM, host *Services, value tjs.Value, budget *uint64) (layerExPoint, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return layerExPoint{}, nil
	}
	if host != nil {
		host.ensureInit()
		host.mu.RLock()
		state := host.LayerExObjects[value.ObjectRef().Object]
		host.mu.RUnlock()
		if state != nil && state.kind == "PointF" {
			return state.value.(layerExPoint), nil
		}
	}
	read := func(name, index string) (float64, error) {
		var member tjs.Value
		var found bool
		for i := 0; i < 2; i++ {
			var err error
			member, found, err = layerExMember(vm, host, value, name, budget)
			if err != nil {
				return 0, err
			}
		}
		if found {
			n, err := member.Real()
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return 0, nil
			}
			return n, nil
		}
		return layerExFloatMember(vm, host, value, index, 0, budget), nil
	}
	y, err := read("y", "1")
	if err != nil {
		return layerExPoint{}, err
	}
	x, err := read("x", "0")
	if err != nil {
		return layerExPoint{}, err
	}
	return layerExPoint{x: x, y: y}, nil
}

func layerExRectFromValue(vm *tjs.VM, host *Services, value tjs.Value, budget *uint64) (layerExRect, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return layerExRect{}, nil
	}
	if host != nil {
		host.ensureInit()
		host.mu.RLock()
		state := host.LayerExObjects[value.ObjectRef().Object]
		host.mu.RUnlock()
		if state != nil && state.kind == "RectF" {
			return state.value.(layerExRect), nil
		}
	}
	names := [...]string{"x", "y", "width", "height"}
	values := [4]float64{}
	indices := [...]string{"0", "1", "2", "3"}
	for i, name := range names {
		member, found, err := layerExMember(vm, host, value, name, budget)
		if err != nil {
			return layerExRect{}, err
		}
		if !found {
			values[i] = layerExFloatMember(vm, host, value, indices[i], 0, budget)
			continue
		}
		values[i], err = member.Real()
		if err != nil || math.IsNaN(values[i]) || math.IsInf(values[i], 0) {
			values[i] = 0
		}
	}
	return layerExRect{x: values[0], y: values[1], w: values[2], h: values[3]}, nil
}
func layerExSequenceLength(vm *tjs.VM, host *Services, value tjs.Value, budget *uint64) (int64, error) {
	if vm != nil {
		if length, err := vm.GetArrayLength(value); err == nil {
			return length, nil
		}
	}
	lengthValue, found, err := layerExMember(vm, host, value, "length", budget)
	if err != nil || !found {
		return 0, fmt.Errorf("value requires an array")
	}
	length, err := lengthValue.Int()
	if err != nil || length < 0 || length > 1<<20 {
		return 0, fmt.Errorf("invalid array length")
	}
	return length, nil
}

func layerExPointsFromValue(vm *tjs.VM, host *Services, value tjs.Value, budget *uint64) ([]layerExPoint, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return nil, fmt.Errorf("point list requires an array or object")
	}
	length, err := layerExSequenceLength(vm, host, value, budget)
	if err != nil {
		return nil, err
	}
	points := make([]layerExPoint, 0, int(length))
	for i := int64(0); i < length; i++ {
		item, found, err := layerExMember(vm, host, value, fmt.Sprintf("%d", i), budget)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		point, err := layerExPointFromValue(vm, host, item, budget)
		if err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	return points, nil
}

func layerExRectsFromValue(vm *tjs.VM, host *Services, value tjs.Value, budget *uint64) ([]layerExRect, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return nil, fmt.Errorf("rectangle list requires an array or object")
	}
	length, err := layerExSequenceLength(vm, host, value, budget)
	if err != nil {
		return nil, err
	}
	rects := make([]layerExRect, 0, int(length))
	for i := int64(0); i < length; i++ {
		item, found, err := layerExMember(vm, host, value, fmt.Sprintf("%d", i), budget)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		rect, err := layerExRectFromValue(vm, host, item, budget)
		if err != nil {
			return nil, err
		}
		rects = append(rects, rect)
	}
	return rects, nil
}

func layerExColor(value tjs.Value, fallback uint32) uint32 {
	n, err := value.Int()
	if err != nil {
		return fallback
	}
	return uint32(n)
}

func layerExBooleanValue(value bool) tjs.Value {
	if value {
		return tjs.IntegerValue(1)
	}
	return tjs.IntegerValue(0)
}

func layerExStoreObject(s *Services, id tjs.ObjectID, kind string, value any) {
	s.mu.Lock()
	if s.LayerExObjects == nil {
		s.LayerExObjects = make(map[tjs.ObjectID]*layerExObjectState)
	}
	s.LayerExObjects[id] = &layerExObjectState{kind: kind, value: value}
	s.mu.Unlock()
}

func layerExNewObject(vm *tjs.VM, host *Services, name string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if vm == nil {
		return tjs.VoidValue, fmt.Errorf("%s requires a VM", name)
	}
	root, ok := vm.Globals["GdiPlus"]
	if !ok {
		return tjs.VoidValue, fmt.Errorf("GdiPlus is unavailable")
	}
	memberName := name
	if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
		memberName = name[dot+1:]
	}
	class, err := vm.GetMember(root, memberName, host, budget)
	if err != nil {
		return tjs.VoidValue, err
	}
	return vm.Construct(class, args, host, budget)
}

func layerExRectValue(vm *tjs.VM, host *Services, rect layerExRect, budget *uint64) (tjs.Value, error) {
	args := []tjs.Value{tjs.RealValue(rect.x), tjs.RealValue(rect.y), tjs.RealValue(rect.w), tjs.RealValue(rect.h)}
	return layerExNewObject(vm, host, "GdiPlus.RectF", args, budget)
}

func layerExPointValue(vm *tjs.VM, host *Services, point layerExPoint, budget *uint64) (tjs.Value, error) {
	args := []tjs.Value{tjs.RealValue(point.x), tjs.RealValue(point.y)}
	return layerExNewObject(vm, host, "GdiPlus.PointF", args, budget)
}

func layerExRectBounds(rects []layerExSubpath) layerExRect {
	if len(rects) == 0 {
		return layerExRect{}
	}
	first := true
	var minX, minY, maxX, maxY float64
	for _, path := range rects {
		for _, point := range path.points {
			if first {
				minX, maxX, minY, maxY = point.x, point.x, point.y, point.y
				first = false
				continue
			}
			minX, maxX = math.Min(minX, point.x), math.Max(maxX, point.x)
			minY, maxY = math.Min(minY, point.y), math.Max(maxY, point.y)
		}
	}
	if first {
		return layerExRect{}
	}
	return layerExRect{x: minX, y: minY, w: maxX - minX, h: maxY - minY}
}

func layerExTransformRect(m layerExAffine, rect layerExRect) layerExRect {
	points := []layerExPoint{
		{rect.x, rect.y}, {rect.x + rect.w, rect.y},
		{rect.x, rect.y + rect.h}, {rect.x + rect.w, rect.y + rect.h},
	}
	for i := range points {
		points[i] = m.apply(points[i])
	}
	return layerExRectBounds([]layerExSubpath{{points: points}})
}

func layerExClampRect(rect layerExRect, width, height int32) Rect {
	left := int32(math.Floor(rect.x))
	top := int32(math.Floor(rect.y))
	right := int32(math.Ceil(rect.x + rect.w))
	bottom := int32(math.Ceil(rect.y + rect.h))
	return Rect{X: left, Y: top, Width: right - left, Height: bottom - top}.Intersect(Rect{Width: width, Height: height})
}

func layerExArcPoints(x, y, w, h, start, sweep float64) []layerExPoint {
	count := int(math.Ceil(math.Abs(sweep) / 8))
	if count < 2 {
		count = 2
	}
	if count > 2048 {
		count = 2048
	}
	points := make([]layerExPoint, count+1)
	cx, cy := x+w/2, y+h/2
	rx, ry := w/2, h/2
	for i := range points {
		ratio := float64(i) / float64(count)
		angle := (start + sweep*ratio) * math.Pi / 180
		points[i] = layerExPoint{cx + rx*math.Cos(angle), cy + ry*math.Sin(angle)}
	}
	return points
}

func layerExBezierPoints(p0, p1, p2, p3 layerExPoint, count int) []layerExPoint {
	if count < 2 {
		count = 2
	}
	points := make([]layerExPoint, count+1)
	for i := range points {
		t := float64(i) / float64(count)
		u := 1 - t
		points[i] = layerExPoint{
			x: u*u*u*p0.x + 3*u*u*t*p1.x + 3*u*t*t*p2.x + t*t*t*p3.x,
			y: u*u*u*p0.y + 3*u*u*t*p1.y + 3*u*t*t*p2.y + t*t*t*p3.y,
		}
	}
	return points
}

func layerExCardinalPoints(points []layerExPoint, closed bool, tension float64) []layerExPoint {
	if len(points) < 2 {
		return append([]layerExPoint(nil), points...)
	}
	if tension < -1 {
		tension = -1
	}
	if tension > 1 {
		tension = 1
	}
	segments := len(points) - 1
	if closed {
		segments = len(points)
	}
	result := make([]layerExPoint, 0, segments*16+1)
	for i := 0; i < segments; i++ {
		get := func(index int) layerExPoint {
			if closed {
				index = (index%len(points) + len(points)) % len(points)
				return points[index]
			}
			if index < 0 {
				return points[0]
			}
			if index >= len(points) {
				return points[len(points)-1]
			}
			return points[index]
		}
		p0, p1, p2, p3 := get(i-1), get(i), get(i+1), get(i+2)
		for step := 0; step < 16; step++ {
			t := float64(step) / 16
			t2, t3 := t*t, t*t*t
			result = append(result, layerExPoint{
				x: 0.5 * ((2 * p1.x) + (-p0.x+p2.x)*t + (2*p0.x-5*p1.x+4*p2.x-p3.x)*t2 + (-p0.x+3*p1.x-3*p2.x+p3.x)*t3),
				y: 0.5 * ((2 * p1.y) + (-p0.y+p2.y)*t + (2*p0.y-5*p1.y+4*p2.y-p3.y)*t2 + (-p0.y+3*p1.y-3*p2.y+p3.y)*t3),
			})
		}
	}
	result = append(result, points[len(points)-1])
	return result
}

func layerExPointInPolygon(point layerExPoint, points []layerExPoint) bool {
	inside := false
	if len(points) < 3 {
		return false
	}
	j := len(points) - 1
	for i := range points {
		pi, pj := points[i], points[j]
		if (pi.y > point.y) != (pj.y > point.y) && point.x < (pj.x-pi.x)*(point.y-pi.y)/(pj.y-pi.y)+pi.x {
			inside = !inside
		}
		j = i
	}
	return inside
}

func layerExDistanceToSegment(point, a, b layerExPoint) float64 {
	vx, vy := b.x-a.x, b.y-a.y
	wx, wy := point.x-a.x, point.y-a.y
	length := vx*vx + vy*vy
	if length <= 1e-12 {
		return math.Hypot(wx, wy)
	}
	t := (wx*vx + wy*vy) / length
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	dx, dy := point.x-(a.x+t*vx), point.y-(a.y+t*vy)
	return math.Hypot(dx, dy)
}

func layerExColorRGBA(value uint32) [4]byte {
	return [4]byte{byte(value >> 16), byte(value >> 8), byte(value), byte(value >> 24)}
}

func layerExGradientColor(paint layerExPaint, t float64) [4]byte {
	colors := paint.gradientColors
	stops := paint.gradientStops
	if len(colors) < 2 || len(stops) != len(colors) {
		one, two := layerExColorRGBA(paint.color), layerExColorRGBA(paint.color2)
		return [4]byte{
			byte(float64(one[0])*(1-t) + float64(two[0])*t),
			byte(float64(one[1])*(1-t) + float64(two[1])*t),
			byte(float64(one[2])*(1-t) + float64(two[2])*t),
			byte(float64(one[3])*(1-t) + float64(two[3])*t),
		}
	}
	if t <= stops[0] {
		return layerExColorRGBA(colors[0])
	}
	for i := 1; i < len(colors); i++ {
		if t <= stops[i] {
			span := stops[i] - stops[i-1]
			local := 0.0
			if span > 0 {
				local = (t - stops[i-1]) / span
			}
			one, two := layerExColorRGBA(colors[i-1]), layerExColorRGBA(colors[i])
			return [4]byte{
				byte(float64(one[0])*(1-local) + float64(two[0])*local),
				byte(float64(one[1])*(1-local) + float64(two[1])*local),
				byte(float64(one[2])*(1-local) + float64(two[2])*local),
				byte(float64(one[3])*(1-local) + float64(two[3])*local),
			}
		}
	}
	return layerExColorRGBA(colors[len(colors)-1])
}

func layerExModulo(value, size int) int {
	if size <= 0 {
		return 0
	}
	value %= size
	if value < 0 {
		value += size
	}
	return value
}

func layerExHatchPrimary(style, x, y int) bool {
	x = layerExModulo(x, 8)
	y = layerExModulo(y, 8)
	switch style {
	case 0:
		return y == 4
	case 1:
		return x == 4
	case 2:
		return layerExModulo(x-y, 8) <= 1
	case 3:
		return layerExModulo(x+y, 8) <= 1
	case 4:
		return x == 4 || y == 4
	case 5:
		return layerExModulo(x-y, 8) <= 1 || layerExModulo(x+y, 8) <= 1
	case 6:
		return x == 0 && y == 0
	case 7:
		return (x*17+y*31)%100 < 10
	case 8:
		return (x*17+y*31)%100 < 20
	case 9:
		return (x*17+y*31)%100 < 25
	case 10:
		return (x*17+y*31)%100 < 30
	case 11:
		return (x*17+y*31)%100 < 40
	case 12:
		return (x+y)&1 == 0
	case 13:
		return (x*17+y*31)%100 < 60
	case 14:
		return (x*17+y*31)%100 < 70
	case 15:
		return (x*17+y*31)%100 < 75
	case 16:
		return (x*17+y*31)%100 < 80
	case 17:
		return (x*17+y*31)%100 < 90
	case 36:
		return y == 0 || y == 2
	case 40:
		return x == 0 || x == 1 || y == 0 || y == 1
	case 43:
		return x == 0 && y == 0
	case 48:
		return x%2 == 0 || y%2 == 0
	default:
		return layerExModulo(x+y, 8) <= 1
	}
}

func layerExTextureCoordinate(value float64, size, wrapMode int, flipAxis bool) int {
	if size <= 0 {
		return 0
	}
	if wrapMode == 4 {
		index := int(math.Floor(value))
		if index < 0 {
			return 0
		}
		if index >= size {
			return size - 1
		}
		return index
	}
	tile := int(math.Floor(value / float64(size)))
	index := layerExModulo(int(math.Floor(value)), size)
	if flipAxis && tile&1 != 0 {
		index = size - 1 - index
	}
	return index
}

func layerExPaintColor(paint layerExPaint, point layerExPoint) [4]byte {
	if paint.brushType == layerExPathGradient {
		radius := paint.radius
		if radius <= 0 {
			radius = 1
		}
		t := math.Hypot(point.x-paint.center.x, point.y-paint.center.y) / radius
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
		return layerExGradientColor(paint, t)
	}
	if paint.brushType == layerExLinearGradient {
		vx, vy := paint.p2.x-paint.p1.x, paint.p2.y-paint.p1.y
		denom := vx*vx + vy*vy
		t := 0.0
		if denom > 1e-12 {
			t = ((point.x-paint.p1.x)*vx + (point.y-paint.p1.y)*vy) / denom
		}
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
		return layerExGradientColor(paint, t)
	}
	if paint.brushType == layerExHatch {
		x, y := int(math.Floor(point.x)), int(math.Floor(point.y))
		if !layerExHatchPrimary(paint.hatch, x, y) {
			return layerExColorRGBA(paint.color2)
		}
	}
	if paint.brushType == layerExTexture && paint.texture != nil && paint.texture.Width > 0 && paint.texture.Height > 0 {
		x, y := point.x, point.y
		if paint.hasTextureRect && paint.textureRect.w != 0 && paint.textureRect.h != 0 {
			x = (x - paint.textureRect.x) * float64(paint.texture.Width) / paint.textureRect.w
			y = (y - paint.textureRect.y) * float64(paint.texture.Height) / paint.textureRect.h
		}
		ix := layerExTextureCoordinate(x, int(paint.texture.Width), paint.wrapMode, paint.wrapMode == 1 || paint.wrapMode == 3)
		iy := layerExTextureCoordinate(y, int(paint.texture.Height), paint.wrapMode, paint.wrapMode == 2 || paint.wrapMode == 3)
		i := (iy*int(paint.texture.Width) + ix) * 4
		if i+3 < len(paint.texture.RGBA) {
			return [4]byte{paint.texture.RGBA[i], paint.texture.RGBA[i+1], paint.texture.RGBA[i+2], paint.texture.RGBA[i+3]}
		}
	}
	return layerExColorRGBA(paint.color)
}

func layerExStrokeCovered(point layerExPoint, paths []layerExSubpath, paint layerExPaint) float64 {
	width := paint.width
	if width <= 0 {
		width = 1
	}
	pattern := append([]float64(nil), paint.dash...)
	if len(pattern) == 0 {
		switch paint.dashStyle {
		case 1:
			pattern = []float64{3, 1}
		case 2:
			pattern = []float64{1, 1}
		case 3:
			pattern = []float64{3, 1, 1, 1}
		case 4:
			pattern = []float64{3, 1, 1, 1, 1, 1}
		}
	}
	for i := range pattern {
		if pattern[i] < 0 {
			pattern[i] = 0
		}
		if pattern[i] > 0 {
			pattern[i] *= width
		}
	}
	totalPattern := 0.0
	for _, length := range pattern {
		totalPattern += length
	}
	for _, path := range paths {
		cumulative := 0.0
		segmentCount := len(path.points) - 1
		if path.closed {
			segmentCount = len(path.points)
		}
		for i := 0; i < segmentCount; i++ {
			start := path.points[i]
			end := path.points[(i+1)%len(path.points)]
			dx, dy := end.x-start.x, end.y-start.y
			lengthSquared := dx*dx + dy*dy
			segmentLength := math.Sqrt(lengthSquared)
			if segmentLength <= 1e-9 {
				continue
			}
			rawT := ((point.x-start.x)*dx + (point.y-start.y)*dy) / lengthSquared
			t := rawT
			if t < 0 {
				t = 0
			} else if t > 1 {
				t = 1
			}
			if !path.closed && rawT < 0 || !path.closed && rawT > 1 {
				cap := paint.lineCap
				if rawT > 1 {
					cap = paint.endCap
				}
				if cap == 0 {
					continue
				}
				if cap == 3 {
					along := rawT * segmentLength
					if along < 0 {
						along = -along
					} else {
						along -= segmentLength
					}
					perpendicular := math.Abs((point.x-start.x)*dy-(point.y-start.y)*dx) / segmentLength
					if along > width/2 || perpendicular > width/2 {
						continue
					}
				}
			}
			closest := layerExPoint{x: start.x + t*dx, y: start.y + t*dy}
			if math.Hypot(point.x-closest.x, point.y-closest.y) <= width/2 {
				if totalPattern == 0 {
					return 1
				}
				distance := math.Mod(cumulative+t*segmentLength+paint.dashOffset, totalPattern)
				if distance < 0 {
					distance += totalPattern
				}
				visible := true
				cursor := 0.0
				for index, length := range pattern {
					if distance < cursor+length {
						visible = index%2 == 0
						break
					}
					cursor += length
				}
				if visible {
					return 1
				}
			}
			cumulative += segmentLength
		}
	}
	return 0
}

func layerExRasterShape(targets [][]byte, width, height int32, paths []layerExSubpath, transform layerExAffine, paints []layerExPaint, smoothing int64, clip Rect) layerExRect {
	if len(paths) == 0 || len(paints) == 0 || width <= 0 || height <= 0 {
		return layerExRect{}
	}
	samples := 1
	if smoothing > 0 {
		samples = 4
	}
	var overall layerExRect
	for _, paint := range paints {
		paintTransform := transform.multiply(layerExTranslation(paint.offset.x, paint.offset.y))
		transformed := make([]layerExSubpath, len(paths))
		for i, path := range paths {
			transformed[i] = layerExSubpath{closed: path.closed, points: make([]layerExPoint, len(path.points))}
			for j, point := range path.points {
				transformed[i].points[j] = paintTransform.apply(point)
			}
		}
		bounds := layerExRectBounds(transformed)
		if paint.kind == layerExPen {
			padding := paint.width / 2
			bounds = layerExRect{x: bounds.x - padding, y: bounds.y - padding, w: bounds.w + padding*2, h: bounds.h + padding*2}
		}
		overall = layerExUnionRect(overall, bounds)
		region := layerExClampRect(bounds, width, height).Intersect(clip)
		if region.Empty() {
			continue
		}
		inv, invertible := paintTransform.invert()
		if !invertible {
			continue
		}
		for y := region.Y; y < region.Bottom(); y++ {
			for x := region.X; x < region.Right(); x++ {
				coverage := 0.0
				for sy := 0; sy < samples; sy++ {
					for sx := 0; sx < samples; sx++ {
						world := layerExPoint{x: float64(x) + (float64(sx)+0.5)/float64(samples), y: float64(y) + (float64(sy)+0.5)/float64(samples)}
						local := inv.apply(world)
						covered := false
						if paint.kind == layerExBrush {
							for _, path := range paths {
								if path.closed && layerExPointInPolygon(local, path.points) {
									covered = true
									break
								}
							}
						} else {
							covered = layerExStrokeCovered(local, paths, paint) > 0
						}
						if covered {
							coverage++
						}
					}
				}
				coverage /= float64(samples * samples)
				if coverage <= 0 {
					continue
				}
				local := inv.apply(layerExPoint{x: float64(x) + 0.5, y: float64(y) + 0.5})
				c := layerExPaintColor(paint, local)
				sourceAlpha := uint8(float64(c[3]) * coverage)
				if sourceAlpha == 0 {
					continue
				}
				source := []byte{c[0], c[1], c[2], sourceAlpha}
				for _, dst := range targets {
					i := (int(y)*int(width) + int(x)) * 4
					if i+3 < len(dst) {
						blendPixelForTarget(dst[i:i+4], source, 255, LayerAlpha, LayerAlpha)
					}
				}
			}
		}
	}
	return overall
}

func layerExBlendImage(targets [][]byte, width, height int32, source assets.Image, sourceRect layerExRect, transform layerExAffine, smoothing int64, clip Rect) layerExRect {
	if source.Width == 0 || source.Height == 0 || len(source.RGBA) < int(source.Width*source.Height*4) || sourceRect.w == 0 || sourceRect.h == 0 {
		return layerExRect{}
	}
	corners := []layerExPoint{{0, 0}, {sourceRect.w, 0}, {0, sourceRect.h}, {sourceRect.w, sourceRect.h}}
	worldCorners := make([]layerExPoint, len(corners))
	for i, p := range corners {
		worldCorners[i] = transform.apply(p)
	}
	bounds := layerExRectBounds([]layerExSubpath{{points: worldCorners}})
	region := layerExClampRect(bounds, width, height).Intersect(clip)
	if region.Empty() {
		return bounds
	}
	inv, ok := transform.invert()
	if !ok {
		return bounds
	}
	bilinear := smoothing > 0
	for y := region.Y; y < region.Bottom(); y++ {
		for x := region.X; x < region.Right(); x++ {
			local := inv.apply(layerExPoint{x: float64(x) + 0.5, y: float64(y) + 0.5})
			if local.x < 0 || local.x >= sourceRect.w || local.y < 0 || local.y >= sourceRect.h {
				continue
			}
			sx := sourceRect.x + local.x
			sy := sourceRect.y + local.y
			pixel := layerExSampleImage(source, sx-0.5, sy-0.5, bilinear)
			if pixel[3] == 0 {
				continue
			}
			for _, dst := range targets {
				i := (int(y)*int(width) + int(x)) * 4
				if i+3 < len(dst) {
					blendPixelForTarget(dst[i:i+4], pixel[:], 255, LayerAlpha, LayerAlpha)
				}
			}
		}
	}
	return bounds
}

func layerExSampleImage(source assets.Image, x, y float64, bilinear bool) [4]byte {
	if !bilinear {
		ix, iy := int(math.Floor(x)), int(math.Floor(y))
		if ix < 0 || iy < 0 || ix >= int(source.Width) || iy >= int(source.Height) {
			return [4]byte{}
		}
		i := (iy*int(source.Width) + ix) * 4
		return [4]byte{source.RGBA[i], source.RGBA[i+1], source.RGBA[i+2], source.RGBA[i+3]}
	}
	xFloor, yFloor := math.Floor(x), math.Floor(y)
	fx, fy := x-xFloor, y-yFloor
	x0, y0 := int(xFloor), int(yFloor)
	x1, y1 := x0+1, y0+1
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	if x0 >= int(source.Width) {
		x0 = int(source.Width) - 1
	}
	if y0 >= int(source.Height) {
		y0 = int(source.Height) - 1
	}
	if x1 >= int(source.Width) {
		x1 = int(source.Width) - 1
	}
	if y1 >= int(source.Height) {
		y1 = int(source.Height) - 1
	}
	var accum [4]float64
	for dy, iy := range []int{y0, y1} {
		for dx, ix := range []int{x0, x1} {
			weight := (1 - math.Abs(float64(dx)-fx)) * (1 - math.Abs(float64(dy)-fy))
			i := (iy*int(source.Width) + ix) * 4
			for c := 0; c < 4; c++ {
				accum[c] += float64(source.RGBA[i+c]) * weight
			}
		}
	}
	return [4]byte{byte(math.Round(accum[0])), byte(math.Round(accum[1])), byte(math.Round(accum[2])), byte(math.Round(accum[3]))}
}

func layerExEncodeImage(source assets.Image, mime string) ([]byte, error) {
	if source.Width == 0 || source.Height == 0 {
		return nil, fmt.Errorf("image is empty")
	}
	nrgba := &image.NRGBA{Pix: append([]byte(nil), source.RGBA...), Stride: int(source.Width) * 4, Rect: image.Rect(0, 0, int(source.Width), int(source.Height))}
	var buf bytes.Buffer
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "", "image/png", "png":
		if err := png.Encode(&buf, nrgba); err != nil {
			return nil, err
		}
	case "image/jpeg", "jpeg", "jpg":
		if err := jpeg.Encode(&buf, nrgba, &jpeg.Options{Quality: 95}); err != nil {
			return nil, err
		}
	case "image/gif", "gif":
		if err := gif.Encode(&buf, nrgba, nil); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported image mime %q", mime)
	}
	return buf.Bytes(), nil
}

func layerExDecodeImage(data []byte) (assets.Image, error) {
	imageData, err := assets.DecodeImage(data)
	if err != nil {
		return assets.Image{}, err
	}
	imageData.RGBA = append([]byte(nil), imageData.RGBA...)
	return imageData, nil
}

func layerExImageFromValue(s *Services, value tjs.Value, budget *uint64) (assets.Image, error) {
	if value.Kind() == tjs.KindString {
		data, err := s.readStorage(value.Text())
		if err != nil {
			return assets.Image{}, err
		}
		return layerExDecodeImage(data)
	}
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return assets.Image{}, fmt.Errorf("image requires GdiPlus.Image, Layer, or storage name")
	}
	id := value.ObjectRef().Object
	s.mu.RLock()
	if state := s.LayerExObjects[id]; state != nil && state.kind == "Image" {
		imageState := state.value.(*layerExImage)
		out := imageState.image
		valid := imageState.valid
		out.RGBA = append([]byte(nil), out.RGBA...)
		s.mu.RUnlock()
		if !valid {
			return assets.Image{}, fmt.Errorf("image is empty")
		}
		return out, nil
	}
	layer := s.Layers[id]
	if layer != nil {
		out := assets.Image{Width: uint32(maxInt32(layer.deviceWidth(), 0)), Height: uint32(maxInt32(layer.deviceHeight(), 0)), RGBA: append([]byte(nil), layer.RGBA...)}
		s.mu.RUnlock()
		return out, nil
	}
	s.mu.RUnlock()
	return assets.Image{}, fmt.Errorf("object %d is not an image", id)
}

func layerExPathArc(path *layerExPath, x, y, w, h, start, sweep float64, pie bool) {
	points := layerExArcPoints(x, y, w, h, start, sweep)
	if pie {
		center := layerExPoint{x + w/2, y + h/2}
		points = append([]layerExPoint{center}, points...)
	}
	path.subpaths = append(path.subpaths, layerExSubpath{points: points, closed: pie || math.Abs(sweep) >= 360})
}

func layerExPathAddPolyline(path *layerExPath, points []layerExPoint, closed bool) {
	if len(points) == 0 {
		return
	}
	path.subpaths = append(path.subpaths, layerExSubpath{points: append([]layerExPoint(nil), points...), closed: closed})
}

func layerExPathFromPoints(points []layerExPoint, closed bool) []layerExSubpath {
	if len(points) == 0 {
		return nil
	}
	return []layerExSubpath{{points: append([]layerExPoint(nil), points...), closed: closed}}
}

func layerExColorAt(value uint32, point layerExPoint) color.RGBA {
	c := layerExColorRGBA(value)
	return color.RGBA{R: c[0], G: c[1], B: c[2], A: c[3]}
}

func layerExRecordImage(state *layerExDrawState) assets.Image {
	if state == nil || !state.record || state.recordWidth <= 0 || state.recordHeight <= 0 {
		return assets.Image{}
	}
	return assets.Image{Width: uint32(state.recordWidth), Height: uint32(state.recordHeight), RGBA: append([]byte(nil), state.recordPixels...)}
}

func layerExRecordPayload(state *layerExDrawState) ([]byte, error) {
	imageData := layerExRecordImage(state)
	if imageData.Width == 0 {
		return nil, fmt.Errorf("record is empty")
	}
	payload, err := layerExEncodeImage(imageData, "image/png")
	if err != nil {
		return nil, err
	}
	header, _ := json.Marshal(layerExRecordHeader{Magic: "krkrgo-layerexdraw-record-v1", Width: int32(imageData.Width), Height: int32(imageData.Height)})
	return append(append(header, '\n'), payload...), nil
}

func layerExLoadRecordPayload(data []byte) (assets.Image, error) {
	if index := bytes.IndexByte(data, '\n'); index > 0 {
		var header layerExRecordHeader
		if json.Unmarshal(data[:index], &header) == nil && header.Magic == "krkrgo-layerexdraw-record-v1" {
			return layerExDecodeImage(data[index+1:])
		}
	}
	return layerExDecodeImage(data)
}

func layerExNormalizeImage(imageData assets.Image) assets.Image {
	imageData.RGBA = append([]byte(nil), imageData.RGBA...)
	return imageData
}

func layerExRotateFlip(source assets.Image, mode int64) assets.Image {
	if source.Width == 0 || source.Height == 0 {
		return source
	}
	rotate := int(mode & 3)
	flipX := mode&4 != 0
	flipY := mode&8 != 0
	width, height := source.Width, source.Height
	if rotate&1 != 0 {
		width, height = height, width
	}
	out := assets.Image{Width: width, Height: height, RGBA: make([]byte, int(width*height*4))}
	for y := uint32(0); y < height; y++ {
		for x := uint32(0); x < width; x++ {
			sx, sy := x, y
			switch rotate {
			case 1:
				sx, sy = y, source.Height-1-x
			case 2:
				sx, sy = source.Width-1-x, source.Height-1-y
			case 3:
				sx, sy = source.Width-1-y, x
			}
			if flipX {
				sx = source.Width - 1 - sx
			}
			if flipY {
				sy = source.Height - 1 - sy
			}
			d := (int(y)*int(width) + int(x)) * 4
			s := (int(sy)*int(source.Width) + int(sx)) * 4
			copy(out.RGBA[d:d+4], source.RGBA[s:s+4])
		}
	}
	return out
}
func (s *Services) layerExDrawStateLocked(id tjs.ObjectID, l *Layer) *layerExDrawState {
	if s.LayerExDraws == nil {
		s.LayerExDraws = make(map[tjs.ObjectID]*layerExDrawState)
	}
	state := s.LayerExDraws[id]
	if state == nil {
		state = &layerExDrawState{
			updateWhenDraw:    true,
			smoothingMode:     1,
			textRenderingHint: 4,
			transform:         layerExIdentity(),
			viewTransform:     layerExIdentity(),
		}
		s.LayerExDraws[id] = state
	}
	if state.record {
		width, height := l.deviceWidth(), l.deviceHeight()
		if state.recordWidth != width || state.recordHeight != height || len(state.recordPixels) != int(width*height*4) {
			state.recordWidth, state.recordHeight = width, height
			if width > 0 && height > 0 {
				state.recordPixels = make([]byte, int(width*height*4))
			} else {
				state.recordPixels = nil
			}
		}
	}
	return state
}

func (s *Services) layerExClip(l *Layer) Rect {
	width, height := l.deviceWidth(), l.deviceHeight()
	clip := Rect{Width: width, Height: height}
	if value, ok := l.Clip(); ok {
		clip = clip.Intersect(value)
	}
	return clip
}

func (s *Services) layerExTargetsLocked(state *layerExDrawState, l *Layer) ([][]byte, error) {
	if len(l.RGBA) == 0 {
		if l.ImageWidth <= 0 || l.ImageHeight <= 0 {
			return nil, nil
		}
		if err := resizeLayerImage(l, l.ImageWidth, l.ImageHeight); err != nil {
			return nil, err
		}
	}
	targets := [][]byte{l.RGBA}
	if state.record {
		width, height := l.deviceWidth(), l.deviceHeight()
		if state.recordWidth != width || state.recordHeight != height || len(state.recordPixels) != int(width*height*4) {
			state.recordWidth, state.recordHeight = width, height
			state.recordPixels = make([]byte, int(width*height*4))
		}
		targets = append(targets, state.recordPixels)
	}
	return targets, nil
}

func (s *Services) layerExMarkLocked(state *layerExDrawState, l *Layer, rect layerExRect) {
	if rect.w <= 0 || rect.h <= 0 {
		return
	}
	l.markImageDirtyRect(layerExClampRect(rect, l.deviceWidth(), l.deviceHeight()))
	if state.updateWhenDraw {
		s.queueLayerPaint(l)
	}
}

func (s *Services) layerExCurrentTransforms(state *layerExDrawState) (layerExAffine, layerExAffine) {
	return state.transform.multiply(state.viewTransform), state.transform
}

func (s *Services) layerExRedrawRecordLocked(state *layerExDrawState, l *Layer) layerExRect {
	if state == nil || !state.record || len(state.recordPixels) == 0 {
		return layerExRect{}
	}
	if len(l.RGBA) == 0 && l.ImageWidth > 0 && l.ImageHeight > 0 {
		if err := resizeLayerImage(l, l.ImageWidth, l.ImageHeight); err != nil {
			return layerExRect{}
		}
	}
	targets := [][]byte{l.RGBA}
	rect := layerExBlendImage(targets, l.deviceWidth(), l.deviceHeight(),
		assets.Image{Width: uint32(state.recordWidth), Height: uint32(state.recordHeight), RGBA: state.recordPixels},
		layerExRect{w: float64(state.recordWidth), h: float64(state.recordHeight)},
		state.viewTransform, state.smoothingMode, s.layerExClip(l))
	s.layerExMarkLocked(state, l, rect)
	return rect
}

func (s *Services) layerExLayerForContext(context tjs.Value, operation string) (*Layer, tjs.ObjectID, error) {
	id, err := layerExObjectID(context, "LayerExDraw."+operation)
	if err != nil {
		return nil, 0, err
	}
	s.ensureInit()
	s.mu.RLock()
	l := s.Layers[id]
	s.mu.RUnlock()
	if l == nil {
		return nil, id, fmt.Errorf("LayerExDraw.%s: context is not a Layer", operation)
	}
	return l, id, nil
}

func (s *Services) layerExDrawNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	l, id, err := s.layerExLayerForContext(context, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	s.ensureInit()
	if strings.HasPrefix(operation, "get:") {
		key := strings.TrimPrefix(operation, "get:")
		s.mu.Lock()
		state := s.layerExDrawStateLocked(id, l)
		var value tjs.Value
		switch key {
		case "updateWhenDraw":
			value = tjs.IntegerValue(boolInt(state.updateWhenDraw))
		case "smoothingMode":
			value = tjs.IntegerValue(state.smoothingMode)
		case "textRenderingHint":
			value = tjs.IntegerValue(state.textRenderingHint)
		case "record":
			value = tjs.IntegerValue(boolInt(state.record))
		default:
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("unknown LayerExDraw property %q", key)
		}
		s.mu.Unlock()
		return value, nil
	}
	if strings.HasPrefix(operation, "set:") {
		key := strings.TrimPrefix(operation, "set:")
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.%s requires one value", operation)
		}
		s.mu.Lock()
		state := s.layerExDrawStateLocked(id, l)
		switch key {
		case "updateWhenDraw":
			state.updateWhenDraw, err = layerExBool(args[0], "LayerExDraw.updateWhenDraw")
		case "smoothingMode", "textRenderingHint":
			var n int64
			n, err = layerExInt(args[0], "LayerExDraw."+key)
			if err == nil {
				if key == "smoothingMode" {
					state.smoothingMode = n
				} else {
					state.textRenderingHint = n
				}
			}
		case "record":
			var enabled bool
			enabled, err = layerExBool(args[0], "LayerExDraw.record")
			if err == nil {
				state.record = enabled
				if enabled {
					state.recordWidth, state.recordHeight = l.deviceWidth(), l.deviceHeight()
					if state.recordWidth > 0 && state.recordHeight > 0 {
						state.recordPixels = make([]byte, int(state.recordWidth*state.recordHeight*4))
					}
				} else {
					state.recordPixels = nil
					state.recordWidth, state.recordHeight = 0, 0
				}
			}
		default:
			err = fmt.Errorf("unknown LayerExDraw property %q", key)
		}
		s.mu.Unlock()
		return tjs.VoidValue, err
	}

	switch operation {
	case "setViewTransform", "setTransform":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.%s requires a Matrix", operation)
		}
		matrix, err := s.layerExMatrixFromValueWithVM(vm, args[0], budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		state := s.layerExDrawStateLocked(id, l)
		if operation == "setViewTransform" {
			state.viewTransform = matrix
		} else {
			state.transform = matrix
		}
		if state.record {
			s.layerExRedrawRecordLocked(state, l)
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "resetViewTransform", "resetTransform":
		s.mu.Lock()
		state := s.layerExDrawStateLocked(id, l)
		if operation == "resetViewTransform" {
			state.viewTransform = layerExIdentity()
		} else {
			state.transform = layerExIdentity()
		}
		if state.record {
			s.layerExRedrawRecordLocked(state, l)
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "rotateViewTransform", "rotateTransform", "scaleViewTransform", "scaleTransform", "translateViewTransform", "translateTransform":
		if len(args) < 1 || (strings.Contains(operation, "scale") || strings.Contains(operation, "translate")) && len(args) != 2 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.%s has invalid arguments", operation)
		}
		var values []float64
		for i := range args {
			n, e := layerExFloat(args[i], "LayerExDraw."+operation)
			if e != nil {
				return tjs.VoidValue, e
			}
			values = append(values, n)
		}
		s.mu.Lock()
		state := s.layerExDrawStateLocked(id, l)
		target := &state.transform
		if strings.Contains(operation, "View") {
			target = &state.viewTransform
		}
		switch {
		case strings.Contains(operation, "rotate"):
			*target = target.multiply(layerExRotation(values[0]))
		case strings.Contains(operation, "scale"):
			*target = target.multiply(layerExScale(values[0], values[1]))
		default:
			*target = target.multiply(layerExTranslation(values[0], values[1]))
		}
		if state.record {
			s.layerExRedrawRecordLocked(state, l)
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "clear":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.clear requires an ARGB color")
		}
		colorValue := layerExColor(args[0], 0)
		s.mu.Lock()
		state := s.layerExDrawStateLocked(id, l)
		targets, err := s.layerExTargetsLocked(state, l)
		if err == nil {
			c := layerExColorRGBA(colorValue)
			clip := s.layerExClip(l)
			for targetIndex, target := range targets {
				targetWidth, targetHeight := l.deviceWidth(), l.deviceHeight()
				if targetIndex > 0 {
					targetWidth, targetHeight = state.recordWidth, state.recordHeight
				}
				targetClip := clip.Intersect(Rect{Width: targetWidth, Height: targetHeight})
				for y := targetClip.Y; y < targetClip.Bottom(); y++ {
					for x := targetClip.X; x < targetClip.Right(); x++ {
						i := (int(y)*int(targetWidth) + int(x)) * 4
						if i+3 < len(target) {
							target[i], target[i+1], target[i+2], target[i+3] = c[0], c[1], c[2], c[3]
						}
					}
				}
			}
			s.layerExMarkLocked(state, l, layerExRect{w: float64(l.deviceWidth()), h: float64(l.deviceHeight())})
		}
		s.mu.Unlock()
		return tjs.VoidValue, err
	case "drawArc", "drawPie", "drawBezier", "drawBeziers", "drawClosedCurve", "drawClosedCurve2", "drawCurve", "drawCurve2", "drawCurve3", "drawEllipse", "drawLine", "drawLines", "drawPolygon", "drawRectangle", "drawRectangles", "drawPath":
		return s.layerExDrawShape(vm, l, id, operation, args, budget)
	case "drawString", "drawPathString", "drawPathString2":
		return s.layerExDrawString(vm, l, id, operation, args, budget)
	case "measureString", "measureStringInternal", "measureString2", "measureStringInternal2":
		return s.layerExMeasureString(vm, operation, args, budget)
	case "drawImage", "drawImageRect", "drawImageStretch", "drawImageAffine":
		return s.layerExDrawImage(vm, l, id, operation, args, budget)
	case "redrawRecord":
		s.mu.Lock()
		state := s.layerExDrawStateLocked(id, l)
		s.layerExRedrawRecordLocked(state, l)
		s.mu.Unlock()
		return layerExBooleanValue(state.record), nil
	case "getRecordImage":
		s.mu.RLock()
		state := s.LayerExDraws[id]
		imageData := layerExRecordImage(state)
		s.mu.RUnlock()
		if imageData.Width == 0 {
			return tjs.VoidValue, nil
		}
		value, err := layerExNewObject(vm, s, "GdiPlus.Image", nil, budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		s.LayerExObjects[value.ObjectRef().Object] = &layerExObjectState{kind: "Image", value: &layerExImage{image: imageData, valid: true}}
		s.mu.Unlock()
		return value, nil
	case "saveRecord":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.saveRecord requires a filename")
		}
		s.mu.RLock()
		state := s.LayerExDraws[id]
		payload, err := layerExRecordPayload(state)
		s.mu.RUnlock()
		if err != nil {
			return layerExBooleanValue(false), nil
		}
		err = s.writeSave(args[0].Text(), payload, 0)
		return layerExBooleanValue(err == nil), err
	case "loadRecord":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.loadRecord requires a filename")
		}
		data, err := s.readStorage(args[0].Text())
		if err != nil {
			return layerExBooleanValue(false), nil
		}
		imageData, err := layerExLoadRecordPayload(data)
		if err != nil {
			return layerExBooleanValue(false), nil
		}
		s.mu.Lock()
		state := s.layerExDrawStateLocked(id, l)
		state.record = true
		state.recordWidth, state.recordHeight = int32(imageData.Width), int32(imageData.Height)
		state.recordPixels = append([]byte(nil), imageData.RGBA...)
		s.layerExRedrawRecordLocked(state, l)
		s.mu.Unlock()
		return layerExBooleanValue(true), nil
	case "setResolution":
		if len(args) != 2 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.setResolution requires two values")
		}
		x, err := layerExFloat(args[0], "LayerExDraw.setResolution")
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := layerExFloat(args[1], "LayerExDraw.setResolution")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		state := s.layerExDrawStateLocked(id, l)
		state.transform = state.transform.multiply(layerExScale(x/100, y/100))
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "saveImage":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.saveImage requires a filename")
		}
		mime := "image/png"
		if len(args) > 1 && args[1].Kind() != tjs.KindVoid {
			mime = args[1].Text()
		}
		s.mu.RLock()
		imageData := assets.Image{Width: uint32(maxInt32(l.deviceWidth(), 0)), Height: uint32(maxInt32(l.deviceHeight(), 0)), RGBA: append([]byte(nil), l.RGBA...)}
		s.mu.RUnlock()
		payload, err := layerExEncodeImage(imageData, mime)
		if err != nil {
			return layerExBooleanValue(false), err
		}
		err = s.writeSave(args[0].Text(), payload, 0)
		return layerExBooleanValue(err == nil), err
	case "getColorRegionRects":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.getColorRegionRects requires a color")
		}
		target := layerExColor(args[0], 0)
		s.mu.RLock()
		imageData := assets.Image{Width: uint32(maxInt32(l.deviceWidth(), 0)), Height: uint32(maxInt32(l.deviceHeight(), 0)), RGBA: append([]byte(nil), l.RGBA...)}
		s.mu.RUnlock()
		rects := layerExColorRegions(imageData, target)
		values := make([]tjs.Value, 0, len(rects))
		for _, rect := range rects {
			value, err := layerExRectValue(vm, s, rect, budget)
			if err != nil {
				return tjs.VoidValue, err
			}
			values = append(values, value)
		}
		return vm.NewArray(values)
	default:
		return tjs.VoidValue, fmt.Errorf("unknown LayerExDraw operation %q", operation)
	}
}

func (s *Services) layerExAppearanceFromValue(value tjs.Value) (*layerExAppearance, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return nil, fmt.Errorf("draw appearance requires GdiPlus.Appearance")
	}
	s.ensureInit()
	s.mu.RLock()
	state := s.LayerExObjects[value.ObjectRef().Object]
	s.mu.RUnlock()
	if state == nil || state.kind != "Appearance" {
		return nil, fmt.Errorf("object %d is not a GdiPlus.Appearance", value.ObjectRef().Object)
	}
	return state.value.(*layerExAppearance), nil
}

func (s *Services) layerExPathFromValue(value tjs.Value) (*layerExPath, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return nil, fmt.Errorf("drawPath requires GdiPlus.Path")
	}
	s.ensureInit()
	s.mu.RLock()
	state := s.LayerExObjects[value.ObjectRef().Object]
	s.mu.RUnlock()
	if state == nil || state.kind != "Path" {
		return nil, fmt.Errorf("object %d is not a GdiPlus.Path", value.ObjectRef().Object)
	}
	return state.value.(*layerExPath), nil
}

func layerExPaintFromValue(vm *tjs.VM, s *Services, value tjs.Value, kind int, widthOrOption tjs.Value, budget *uint64) (layerExPaint, error) {
	paint := layerExPaint{kind: kind, brushType: layerExSolid, color: 0xffffffff, color2: 0xff000000, width: 1, dashStyle: 0}
	var err error
	if value.Kind() != tjs.KindObject || value.IsNull() {
		paint.color = layerExColor(value, 0xffffffff)
	} else {
		paint.brushType = int(layerExIntMember(vm, s, value, "type", layerExSolid, budget))
		paint.color = uint32(layerExIntMember(vm, s, value, "color", 0xffffffff, budget))
		paint.color2 = uint32(layerExIntMember(vm, s, value, "backColor", 0xff000000, budget))
		switch paint.brushType {
		case layerExHatch:
			paint.color = uint32(layerExIntMember(vm, s, value, "foreColor", int64(paint.color), budget))
			paint.hatch = int(layerExIntMember(vm, s, value, "hatchStyle", 0, budget))
		case layerExLinearGradient:
			paint.color = uint32(layerExIntMember(vm, s, value, "color1", int64(paint.color), budget))
			paint.color2 = uint32(layerExIntMember(vm, s, value, "color2", int64(paint.color2), budget))
			paint.gradientColors = []uint32{paint.color, paint.color2}
			paint.gradientStops = []float64{0, 1}
			point1, point1Found, point1Err := layerExMember(vm, s, value, "point1", budget)
			if point1Err != nil {
				return layerExPaint{}, point1Err
			}
			point2, point2Found, point2Err := layerExMember(vm, s, value, "point2", budget)
			if point2Err != nil {
				return layerExPaint{}, point2Err
			}
			if point1Found != point2Found {
				return layerExPaint{}, fmt.Errorf("linear gradient requires point1 and point2 together")
			}
			if point1Found {
				paint.p1, err = layerExPointFromValue(vm, s, point1, budget)
				if err != nil {
					return layerExPaint{}, err
				}
				paint.p2, err = layerExPointFromValue(vm, s, point2, budget)
				if err != nil {
					return layerExPaint{}, err
				}
			} else {
				rectValue, rectFound, rectErr := layerExMember(vm, s, value, "rect", budget)
				if rectErr != nil {
					return layerExPaint{}, rectErr
				}
				if !rectFound {
					return layerExPaint{}, fmt.Errorf("linear gradient requires point1, point2, or rect")
				}
				paint.rect, err = layerExRectFromValue(vm, s, rectValue, budget)
				if err != nil {
					return layerExPaint{}, err
				}
				angle := layerExFloatMember(vm, s, value, "angle", 0, budget) * math.Pi / 180
				cx, cy := paint.rect.x+paint.rect.w/2, paint.rect.y+paint.rect.h/2
				length := math.Hypot(paint.rect.w, paint.rect.h) / 2
				paint.p1 = layerExPoint{cx - math.Cos(angle)*length, cy - math.Sin(angle)*length}
				paint.p2 = layerExPoint{cx + math.Cos(angle)*length, cy + math.Sin(angle)*length}
			}
		case layerExTexture:
			paint.wrapMode = int(layerExIntMember(vm, s, value, "wrapMode", 0, budget))
			if imageValue, found, err := layerExMember(vm, s, value, "image", budget); err == nil && found {
				if image, imageErr := layerExImageFromValue(s, imageValue, budget); imageErr == nil {
					paint.texture = &image
				}
			}
			if rectValue, found, err := layerExMember(vm, s, value, "dstRect", budget); err == nil && found {
				if rect, rectErr := layerExRectFromValue(vm, s, rectValue, budget); rectErr == nil {
					paint.textureRect = rect
					paint.hasTextureRect = true
				}
			}
		case layerExPathGradient:
			paint.color = uint32(layerExIntMember(vm, s, value, "centerColor", int64(paint.color), budget))
			paint.color2 = uint32(layerExIntMember(vm, s, value, "surroundColor", int64(paint.color2), budget))
			paint.gradientColors = []uint32{paint.color, paint.color2}
			paint.gradientStops = []float64{0, 1}
			var points []layerExPoint
			if pointsValue, found, pointsErr := layerExMember(vm, s, value, "points", budget); pointsErr == nil && found {
				points, _ = layerExPointsFromValue(vm, s, pointsValue, budget)
			}
			if len(points) > 0 {
				bounds := layerExRectBounds([]layerExSubpath{{points: points}})
				paint.center = layerExPoint{bounds.x + bounds.w/2, bounds.y + bounds.h/2}
				paint.radius = math.Max(bounds.w, bounds.h) / 2
			}
			if centerValue, found, centerErr := layerExMember(vm, s, value, "centerPoint", budget); centerErr == nil && found {
				paint.center, _ = layerExPointFromValue(vm, s, centerValue, budget)
			}
			if colorsValue, found, colorsErr := layerExMember(vm, s, value, "surroundColors", budget); colorsErr == nil && found {
				if colors, colorsErr := layerExColorList(vm, s, colorsValue, budget); colorsErr == nil && len(colors) > 0 {
					paint.color2 = colors[0]
					paint.gradientColors[1] = colors[0]
				}
			}
		}
	}
	if kind == layerExPen {
		if widthOrOption.Kind() != tjs.KindObject || widthOrOption.IsNull() {
			paint.width, _ = layerExFloat(widthOrOption, "Appearance.addPen width")
		} else {
			paint.width = layerExFloatMember(vm, s, widthOrOption, "width", 1, budget)
			paint.dashStyle = int(layerExIntMember(vm, s, widthOrOption, "dashStyle", 0, budget))
			paint.lineCap = int(layerExIntMember(vm, s, widthOrOption, "startCap", 0, budget))
			paint.endCap = int(layerExIntMember(vm, s, widthOrOption, "endCap", int64(paint.lineCap), budget))
			paint.lineJoin = int(layerExIntMember(vm, s, widthOrOption, "lineJoin", 0, budget))
			paint.dashOffset = layerExFloatMember(vm, s, widthOrOption, "dashOffset", 0, budget)
			if dash, found, err := layerExMember(vm, s, widthOrOption, "dashStyle", budget); err == nil && found && dash.Kind() == tjs.KindObject {
				if values, parseErr := layerExDashValues(vm, s, dash, budget); parseErr == nil {
					paint.dash = values
				}
			}
		}
		if paint.width <= 0 {
			paint.width = 1
		}
	}
	return paint, nil
}

func layerExDashValues(vm *tjs.VM, s *Services, value tjs.Value, budget *uint64) ([]float64, error) {
	length, err := layerExSequenceLength(vm, s, value, budget)
	if err != nil {
		return nil, fmt.Errorf("dash pattern requires an array: %w", err)
	}
	if length < 0 || length > 1<<16 {
		return nil, fmt.Errorf("invalid dash pattern length")
	}
	values := make([]float64, 0, int(length))
	for i := int64(0); i < length; i++ {
		item, found, err := layerExMember(vm, s, value, fmt.Sprintf("%d", i), budget)
		if err != nil || !found {
			continue
		}
		n, err := layerExFloat(item, "dash pattern")
		if err != nil {
			return nil, err
		}
		values = append(values, n)
	}
	return values, nil
}
func layerExColorList(vm *tjs.VM, s *Services, value tjs.Value, budget *uint64) ([]uint32, error) {
	length, err := layerExSequenceLength(vm, s, value, budget)
	if err != nil {
		return nil, fmt.Errorf("color list requires an array: %w", err)
	}
	if length < 0 || length > 1<<16 {
		return nil, fmt.Errorf("invalid color list length")
	}
	colors := make([]uint32, 0, int(length))
	for i := int64(0); i < length; i++ {
		item, found, err := layerExMember(vm, s, value, fmt.Sprintf("%d", i), budget)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		colors = append(colors, layerExColor(item, 0))
	}
	return colors, nil
}

func (s *Services) layerExAppearanceArgs(vm *tjs.VM, args []tjs.Value, index int, budget *uint64) (*layerExAppearance, error) {
	if index >= len(args) {
		return nil, fmt.Errorf("missing Appearance argument")
	}
	return s.layerExAppearanceFromValue(args[index])
}

func (s *Services) layerExShapeArgs(vm *tjs.VM, operation string, args []tjs.Value, budget *uint64) ([]layerExSubpath, *layerExAppearance, error) {
	if len(args) == 0 {
		return nil, nil, fmt.Errorf("LayerExDraw.%s requires an Appearance", operation)
	}
	app, err := s.layerExAppearanceFromValue(args[0])
	if err != nil {
		return nil, nil, err
	}
	switch operation {
	case "drawArc", "drawPie":
		if len(args) != 7 {
			return nil, nil, fmt.Errorf("LayerExDraw.%s requires 7 arguments", operation)
		}
		values := make([]float64, 6)
		for i := range values {
			values[i], err = layerExFloat(args[i+1], "LayerExDraw."+operation)
			if err != nil {
				return nil, nil, err
			}
		}
		if operation == "drawPie" {
			path := []layerExPoint{{values[0] + values[2]/2, values[1] + values[3]/2}}
			path = append(path, layerExArcPoints(values[0], values[1], values[2], values[3], values[4], values[5])...)
			return layerExPathFromPoints(path, true), app, nil
		}
		return layerExPathFromPoints(layerExArcPoints(values[0], values[1], values[2], values[3], values[4], values[5]), false), app, nil
	case "drawBezier":
		if len(args) != 9 {
			return nil, nil, fmt.Errorf("LayerExDraw.drawBezier requires 9 arguments")
		}
		values := make([]float64, 8)
		for i := range values {
			values[i], err = layerExFloat(args[i+1], "LayerExDraw.drawBezier")
			if err != nil {
				return nil, nil, err
			}
		}
		return layerExPathFromPoints(layerExBezierPoints(
			layerExPoint{values[0], values[1]}, layerExPoint{values[2], values[3]},
			layerExPoint{values[4], values[5]}, layerExPoint{values[6], values[7]}, 32), false), app, nil
	case "drawBeziers":
		if len(args) != 2 {
			return nil, nil, fmt.Errorf("LayerExDraw.drawBeziers requires an Appearance and point list")
		}
		points, err := layerExPointsFromValue(vm, s, args[1], budget)
		if err != nil || len(points) < 4 {
			return nil, nil, fmt.Errorf("LayerExDraw.drawBeziers requires at least four points")
		}
		out := make([]layerExPoint, 0, (len(points)-1)*16)
		for i := 0; i+3 < len(points); i += 3 {
			segment := layerExBezierPoints(points[i], points[i+1], points[i+2], points[i+3], 24)
			if i != 0 {
				segment = segment[1:]
			}
			out = append(out, segment...)
		}
		return layerExPathFromPoints(out, false), app, nil
	case "drawClosedCurve", "drawClosedCurve2", "drawCurve", "drawCurve2", "drawCurve3":
		if len(args) < 2 {
			return nil, nil, fmt.Errorf("LayerExDraw.%s requires an Appearance and point list", operation)
		}
		points, err := layerExPointsFromValue(vm, s, args[1], budget)
		if err != nil || len(points) < 2 {
			return nil, nil, fmt.Errorf("LayerExDraw.%s requires at least two points", operation)
		}
		tension := 0.5
		closed := strings.HasPrefix(operation, "drawClosed")
		if operation == "drawClosedCurve2" || operation == "drawCurve2" {
			if len(args) != 3 {
				return nil, nil, fmt.Errorf("LayerExDraw.%s requires tension", operation)
			}
			tension, err = layerExFloat(args[2], "LayerExDraw."+operation)
			if err != nil {
				return nil, nil, err
			}
		}
		if operation == "drawCurve3" {
			if len(args) != 5 {
				return nil, nil, fmt.Errorf("LayerExDraw.drawCurve3 requires offset, segment count, and tension")
			}
			offset, offsetErr := layerExInt(args[2], "LayerExDraw.drawCurve3 offset")
			if offsetErr != nil {
				return nil, nil, offsetErr
			}
			count, countErr := layerExInt(args[3], "LayerExDraw.drawCurve3 count")
			if countErr != nil {
				return nil, nil, countErr
			}
			tension, err = layerExFloat(args[4], "LayerExDraw.drawCurve3 tension")
			if err != nil {
				return nil, nil, err
			}
			if offset < 0 || count <= 0 || offset >= int64(len(points)) {
				return nil, nil, fmt.Errorf("LayerExDraw.drawCurve3 range is invalid")
			}
			end := int(offset + count)
			if end > len(points) {
				end = len(points)
			}
			points = points[offset:end]
		}
		return layerExPathFromPoints(layerExCardinalPoints(points, closed, tension), closed), app, nil
	case "drawEllipse":
		if len(args) != 5 {
			return nil, nil, fmt.Errorf("LayerExDraw.drawEllipse requires 5 arguments")
		}
		values := make([]float64, 4)
		for i := range values {
			values[i], err = layerExFloat(args[i+1], "LayerExDraw.drawEllipse")
			if err != nil {
				return nil, nil, err
			}
		}
		return layerExPathFromPoints(layerExArcPoints(values[0], values[1], values[2], values[3], 0, 360), true), app, nil
	case "drawLine":
		if len(args) != 5 {
			return nil, nil, fmt.Errorf("LayerExDraw.drawLine requires 5 arguments")
		}
		values := make([]float64, 4)
		for i := range values {
			values[i], err = layerExFloat(args[i+1], "LayerExDraw.drawLine")
			if err != nil {
				return nil, nil, err
			}
		}
		return layerExPathFromPoints([]layerExPoint{{values[0], values[1]}, {values[2], values[3]}}, false), app, nil
	case "drawLines", "drawPolygon":
		if len(args) != 2 {
			return nil, nil, fmt.Errorf("LayerExDraw.%s requires an Appearance and point list", operation)
		}
		points, err := layerExPointsFromValue(vm, s, args[1], budget)
		if err != nil {
			return nil, nil, err
		}
		return layerExPathFromPoints(points, operation == "drawPolygon"), app, nil
	case "drawRectangle":
		if len(args) != 5 {
			return nil, nil, fmt.Errorf("LayerExDraw.drawRectangle requires 5 arguments")
		}
		values := make([]float64, 4)
		for i := range values {
			values[i], err = layerExFloat(args[i+1], "LayerExDraw.drawRectangle")
			if err != nil {
				return nil, nil, err
			}
		}
		return layerExPathFromPoints([]layerExPoint{
			{values[0], values[1]}, {values[0] + values[2], values[1]},
			{values[0] + values[2], values[1] + values[3]}, {values[0], values[1] + values[3]},
		}, true), app, nil
	case "drawRectangles":
		if len(args) != 2 {
			return nil, nil, fmt.Errorf("LayerExDraw.drawRectangles requires an Appearance and rectangle list")
		}
		rects, err := layerExRectsFromValue(vm, s, args[1], budget)
		if err != nil {
			return nil, nil, err
		}
		paths := make([]layerExSubpath, 0, len(rects))
		for _, rect := range rects {
			paths = append(paths, layerExSubpath{points: []layerExPoint{
				{rect.x, rect.y}, {rect.x + rect.w, rect.y},
				{rect.x + rect.w, rect.y + rect.h}, {rect.x, rect.y + rect.h},
			}, closed: true})
		}
		return paths, app, nil
	case "drawPath":
		if len(args) != 2 {
			return nil, nil, fmt.Errorf("LayerExDraw.drawPath requires Appearance and Path")
		}
		path, err := s.layerExPathFromValue(args[1])
		if err != nil {
			return nil, nil, err
		}
		return path.subpaths, app, nil
	}
	return nil, nil, fmt.Errorf("LayerExDraw.%s shape is unsupported", operation)
}
func layerExUnionRect(a, b layerExRect) layerExRect {
	if a.w <= 0 || a.h <= 0 {
		return b
	}
	if b.w <= 0 || b.h <= 0 {
		return a
	}
	left := math.Min(a.x, b.x)
	top := math.Min(a.y, b.y)
	right := math.Max(a.x+a.w, b.x+b.w)
	bottom := math.Max(a.y+a.h, b.y+b.h)
	return layerExRect{x: left, y: top, w: right - left, h: bottom - top}
}

func (s *Services) layerExDrawShape(vm *tjs.VM, l *Layer, id tjs.ObjectID, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	paths, appearance, err := s.layerExShapeArgs(vm, operation, args, budget)
	if err != nil {
		return tjs.VoidValue, err
	}
	s.mu.Lock()
	state := s.layerExDrawStateLocked(id, l)
	targets, err := s.layerExTargetsLocked(state, l)
	if err != nil {
		s.mu.Unlock()
		return tjs.VoidValue, err
	}
	if len(targets) == 0 {
		s.mu.Unlock()
		return layerExRectValue(vm, s, layerExRect{}, budget)
	}
	calc, base := s.layerExCurrentTransforms(state)
	clip := s.layerExClip(l)
	targetRect := layerExRasterShape(targets[:1], l.deviceWidth(), l.deviceHeight(), paths, calc, appearance.paints, state.smoothingMode, clip)
	if state.record {
		layerExRasterShape(targets[1:], state.recordWidth, state.recordHeight, paths, base, appearance.paints, state.smoothingMode, clip)
	}
	s.layerExMarkLocked(state, l, targetRect)
	s.mu.Unlock()
	return layerExRectValue(vm, s, targetRect, budget)
}

func (s *Services) layerExFontFromValue(value tjs.Value) (*layerExFont, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return nil, fmt.Errorf("text drawing requires GdiPlus.Font")
	}
	s.ensureInit()
	s.mu.RLock()
	state := s.LayerExObjects[value.ObjectRef().Object]
	s.mu.RUnlock()
	if state == nil || state.kind != "Font" {
		return nil, fmt.Errorf("object %d is not a GdiPlus.Font", value.ObjectRef().Object)
	}
	return state.value.(*layerExFont), nil
}

func layerExTextImage(face FontFace, text string) (assets.Image, error) {
	rendered, err := RasterizeText(face, text, color.White)
	if err != nil {
		return assets.Image{}, err
	}
	return assets.Image{Width: uint32(rendered.Rect.Dx()), Height: uint32(rendered.Rect.Dy()), RGBA: append([]byte(nil), rendered.Pix...)}, nil
}

func layerExBlendTextMask(targets [][]byte, width, height int32, source assets.Image, paint layerExPaint, transform layerExAffine, smoothing int64, clip Rect) layerExRect {
	if source.Width == 0 || source.Height == 0 {
		return layerExRect{}
	}
	corners := []layerExPoint{{0, 0}, {float64(source.Width), 0}, {0, float64(source.Height)}, {float64(source.Width), float64(source.Height)}}
	for i := range corners {
		corners[i] = transform.apply(corners[i])
	}
	bounds := layerExRectBounds([]layerExSubpath{{points: corners}})
	region := layerExClampRect(bounds, width, height).Intersect(clip)
	if region.Empty() {
		return bounds
	}
	inv, ok := transform.invert()
	if !ok {
		return bounds
	}
	for y := region.Y; y < region.Bottom(); y++ {
		for x := region.X; x < region.Right(); x++ {
			local := inv.apply(layerExPoint{x: float64(x) + 0.5, y: float64(y) + 0.5})
			pixel := layerExSampleImage(source, local.x, local.y, smoothing > 0)
			if pixel[3] == 0 {
				continue
			}
			c := layerExPaintColor(paint, local)
			sourcePixel := []byte{c[0], c[1], c[2], uint8(int(c[3]) * int(pixel[3]) / 255)}
			for _, dst := range targets {
				i := (int(y)*int(width) + int(x)) * 4
				if i+3 < len(dst) {
					blendPixelForTarget(dst[i:i+4], sourcePixel, 255, LayerAlpha, LayerAlpha)
				}
			}
		}
	}
	return bounds
}

func layerExTextMetrics(s *Services, fontState *layerExFont, text string) (FontFace, TextMetrics, assets.Image, error) {
	style := fontState.style
	state := &FontState{
		Face:      fontState.familyName,
		Height:    -int32(math.Round(fontState.emSize)),
		Bold:      style&1 != 0,
		Italic:    style&2 != 0,
		Underline: style&4 != 0,
		Strikeout: style&8 != 0,
	}
	face, err := s.fontFace(state)
	if err != nil {
		return nil, TextMetrics{}, assets.Image{}, err
	}
	metrics, err := MeasureText(face, text)
	if err != nil {
		return face, TextMetrics{}, assets.Image{}, err
	}
	imageData, err := layerExTextImage(face, text)
	if err != nil {
		return face, metrics, assets.Image{}, err
	}
	return face, metrics, imageData, nil
}

func (s *Services) layerExDrawString(vm *tjs.VM, l *Layer, id tjs.ObjectID, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if len(args) != 5 {
		return tjs.VoidValue, fmt.Errorf("LayerExDraw.%s requires font, Appearance, x, y, and text", operation)
	}
	fontState, err := s.layerExFontFromValue(args[0])
	if err != nil {
		return tjs.VoidValue, err
	}
	appearance, err := s.layerExAppearanceFromValue(args[1])
	if err != nil {
		return tjs.VoidValue, err
	}
	x, err := layerExFloat(args[2], "LayerExDraw."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	y, err := layerExFloat(args[3], "LayerExDraw."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	text := args[4].Text()
	face, _, imageData, err := layerExTextMetrics(s, fontState, text)
	if face != nil {
		face.Close()
	}
	if err != nil {
		return tjs.VoidValue, err
	}
	s.mu.Lock()
	state := s.layerExDrawStateLocked(id, l)
	targets, err := s.layerExTargetsLocked(state, l)
	if err != nil {
		s.mu.Unlock()
		return tjs.VoidValue, err
	}
	if len(targets) == 0 {
		s.mu.Unlock()
		return layerExRectValue(vm, s, layerExRect{}, budget)
	}
	calc, base := s.layerExCurrentTransforms(state)
	clip := s.layerExClip(l)
	var targetRect layerExRect
	for _, paint := range appearance.paints {
		if paint.kind != layerExBrush && operation == "drawString" {
			continue
		}
		transform := calc.multiply(layerExTranslation(x, y)).multiply(layerExTranslation(paint.offset.x, paint.offset.y))
		rect := layerExBlendTextMask(targets[:1], l.deviceWidth(), l.deviceHeight(), imageData, paint, transform, state.smoothingMode, clip)
		targetRect = layerExUnionRect(targetRect, rect)
		if state.record {
			recordTransform := base.multiply(layerExTranslation(x, y)).multiply(layerExTranslation(paint.offset.x, paint.offset.y))
			layerExBlendTextMask(targets[1:], state.recordWidth, state.recordHeight, imageData, paint, recordTransform, state.smoothingMode, clip)
		}
	}
	s.layerExMarkLocked(state, l, targetRect)
	s.mu.Unlock()
	return layerExRectValue(vm, s, targetRect, budget)
}

func (s *Services) layerExMeasureString(vm *tjs.VM, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if len(args) != 2 {
		return tjs.VoidValue, fmt.Errorf("LayerExDraw.%s requires font and text", operation)
	}
	fontState, err := s.layerExFontFromValue(args[0])
	if err != nil {
		return tjs.VoidValue, err
	}
	face, metrics, imageData, err := layerExTextMetrics(s, fontState, args[1].Text())
	if face != nil {
		face.Close()
	}
	if err != nil {
		return tjs.VoidValue, err
	}
	if strings.Contains(operation, "Internal") || strings.Contains(operation, "internal") {
		return layerExRectValue(vm, s, layerExRect{w: float64(imageData.Width), h: float64(imageData.Height)}, budget)
	}
	return layerExRectValue(vm, s, layerExRect{w: float64(metrics.Width), h: float64(metrics.Height)}, budget)
}

func (s *Services) layerExDrawImage(vm *tjs.VM, l *Layer, id tjs.ObjectID, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	var sourceValue tjs.Value
	var sourceRect layerExRect
	var mapping layerExAffine
	switch operation {
	case "drawImage":
		if len(args) != 3 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.drawImage requires x, y, and image")
		}
		x, err := layerExFloat(args[0], "LayerExDraw.drawImage x")
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := layerExFloat(args[1], "LayerExDraw.drawImage y")
		if err != nil {
			return tjs.VoidValue, err
		}
		sourceValue = args[2]
		imageData, err := layerExImageFromValue(s, sourceValue, budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		sourceRect = layerExRect{w: float64(imageData.Width), h: float64(imageData.Height)}
		mapping = layerExTranslation(x, y)
	case "drawImageRect":
		if len(args) != 7 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.drawImageRect requires 7 arguments")
		}
		x, err := layerExFloat(args[0], "LayerExDraw.drawImageRect x")
		if err != nil {
			return tjs.VoidValue, err
		}
		y, err := layerExFloat(args[1], "LayerExDraw.drawImageRect y")
		if err != nil {
			return tjs.VoidValue, err
		}
		sourceValue = args[2]
		values := make([]float64, 4)
		for i := range values {
			values[i], err = layerExFloat(args[i+3], "LayerExDraw.drawImageRect")
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		sourceRect = layerExRect{x: values[0], y: values[1], w: values[2], h: values[3]}
		mapping = layerExTranslation(x, y)
	case "drawImageStretch":
		if len(args) != 9 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.drawImageStretch requires 9 arguments")
		}
		dest := make([]float64, 4)
		var err error
		for i := range dest {
			dest[i], err = layerExFloat(args[i], "LayerExDraw.drawImageStretch destination")
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		sourceValue = args[4]
		sourceValues := make([]float64, 4)
		for i := range sourceValues {
			sourceValues[i], err = layerExFloat(args[i+5], "LayerExDraw.drawImageStretch source")
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		if sourceValues[2] == 0 || sourceValues[3] == 0 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.drawImageStretch source size must be non-zero")
		}
		sourceRect = layerExRect{x: sourceValues[0], y: sourceValues[1], w: sourceValues[2], h: sourceValues[3]}
		mapping = layerExAffineValue(dest[2]/sourceValues[2], 0, 0, dest[3]/sourceValues[3], dest[0], dest[1])
	case "drawImageAffine":
		if len(args) != 12 {
			return tjs.VoidValue, fmt.Errorf("LayerExDraw.drawImageAffine requires 12 arguments")
		}
		sourceValue = args[0]
		values := make([]float64, 11)
		var err error
		for i := range values {
			values[i], err = layerExFloat(args[i+1], "LayerExDraw.drawImageAffine")
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		sourceRect = layerExRect{x: values[0], y: values[1], w: values[2], h: values[3]}
		if values[4] != 0 {
			mapping = layerExAffineValue(values[5], values[6], values[7], values[8], values[9], values[10])
		} else {
			if sourceRect.w == 0 || sourceRect.h == 0 {
				return tjs.VoidValue, fmt.Errorf("LayerExDraw.drawImageAffine source size must be non-zero")
			}
			mapping = layerExAffineValue(
				(values[7]-values[5])/sourceRect.w,
				(values[8]-values[6])/sourceRect.w,
				(values[9]-values[5])/sourceRect.h,
				(values[10]-values[6])/sourceRect.h,
				values[5],
				values[6],
			)
		}
	default:
		return tjs.VoidValue, fmt.Errorf("LayerExDraw.%s is unsupported", operation)
	}
	imageData, err := layerExImageFromValue(s, sourceValue, budget)
	if err != nil {
		return tjs.VoidValue, err
	}
	if sourceRect.w == 0 || sourceRect.h == 0 {
		return tjs.VoidValue, fmt.Errorf("LayerExDraw.%s source size must be non-zero", operation)
	}
	s.mu.Lock()
	state := s.layerExDrawStateLocked(id, l)
	targets, err := s.layerExTargetsLocked(state, l)
	if err != nil {
		s.mu.Unlock()
		return tjs.VoidValue, err
	}
	if len(targets) == 0 {
		s.mu.Unlock()
		return layerExRectValue(vm, s, layerExRect{}, budget)
	}
	calc, base := s.layerExCurrentTransforms(state)
	targetTransform := calc.multiply(mapping)
	baseTransform := base.multiply(mapping)
	clip := s.layerExClip(l)
	targetRect := layerExBlendImage(targets[:1], l.deviceWidth(), l.deviceHeight(), imageData, sourceRect, targetTransform, state.smoothingMode, clip)
	if state.record {
		layerExBlendImage(targets[1:], state.recordWidth, state.recordHeight, imageData, sourceRect, baseTransform, state.smoothingMode, clip)
	}
	s.layerExMarkLocked(state, l, targetRect)
	s.mu.Unlock()
	return layerExRectValue(vm, s, targetRect, budget)
}

func layerExColorRegions(imageData assets.Image, target uint32) []layerExRect {
	width, height := int(imageData.Width), int(imageData.Height)
	if width <= 0 || height <= 0 {
		return nil
	}
	visited := make([]bool, width*height)
	regions := make([]layerExRect, 0)
	match := func(index int) bool {
		if index < 0 || index >= width*height || visited[index] {
			return false
		}
		i := index * 4
		if i+3 >= len(imageData.RGBA) {
			return false
		}
		return uint32(imageData.RGBA[i+3])<<24|uint32(imageData.RGBA[i])<<16|uint32(imageData.RGBA[i+1])<<8|uint32(imageData.RGBA[i+2]) == target
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			start := y*width + x
			if !match(start) {
				continue
			}
			queue := []int{start}
			visited[start] = true
			minX, maxX, minY, maxY := x, x, y, y
			for len(queue) > 0 {
				current := queue[0]
				queue = queue[1:]
				cx, cy := current%width, current/width
				minX, maxX = minInt(minX, cx), maxInt(maxX, cx)
				minY, maxY = minInt(minY, cy), maxInt(maxY, cy)
				for _, next := range []int{current - 1, current + 1, current - width, current + width} {
					if next < 0 || next >= width*height {
						continue
					}
					if (next == current-1 && cx == 0) || (next == current+1 && cx == width-1) || (next == current-width && cy == 0) || (next == current+width && cy == height-1) {
						continue
					}
					if match(next) {
						visited[next] = true
						queue = append(queue, next)
					}
				}
			}
			regions = append(regions, layerExRect{x: float64(minX), y: float64(minY), w: float64(maxX - minX + 1), h: float64(maxY - minY + 1)})
		}
	}
	return regions
}
func (s *Services) gdiPlusNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	s.ensureInit()
	if operation == "@initialize" {
		return tjs.VoidValue, nil
	}
	if strings.HasSuffix(operation, ".finalize") {
		return tjs.VoidValue, nil
	}
	if operation == "addPrivateFont" {
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.addPrivateFont requires a storage name")
		}
		data, err := s.readStorage(args[0].Text())
		if err != nil {
			return tjs.VoidValue, err
		}
		factory, err := ParseOpenType(data)
		if err != nil {
			return tjs.VoidValue, err
		}
		name := args[0].Text()
		if slash := strings.LastIndexAny(name, `/\\`); slash >= 0 {
			name = name[slash+1:]
		}
		if dot := strings.LastIndexByte(name, '.'); dot > 0 {
			name = name[:dot]
		}
		if name == "" {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.addPrivateFont requires a font family name")
		}
		return tjs.VoidValue, s.RegisterFontFactory(name, factory)
	}
	if operation == "getFontList" {
		privateOnly := false
		if len(args) > 0 {
			privateOnly, _ = args[0].Truth()
		}
		names := s.FontBook.Names()
		if privateOnly {
			privateOnlyNames := make([]string, 0, len(names))
			for _, name := range names {
				if strings.HasPrefix(name, "@") {
					privateOnlyNames = append(privateOnlyNames, name)
				}
			}
			names = privateOnlyNames
		}
		values := make([]tjs.Value, len(names))
		for i, name := range names {
			values[i] = tjs.StringValue(name)
		}
		return vm.NewArray(values)
	}
	if strings.HasPrefix(operation, "PointF.") {
		return s.layerExPointNative(vm, context, strings.TrimPrefix(operation, "PointF."), args, budget)
	}
	if strings.HasPrefix(operation, "RectF.") {
		return s.layerExRectNative(vm, context, strings.TrimPrefix(operation, "RectF."), args, budget)
	}
	if strings.HasPrefix(operation, "Matrix.") {
		return s.layerExMatrixNative(vm, context, strings.TrimPrefix(operation, "Matrix."), args, budget)
	}
	if strings.HasPrefix(operation, "Image.") {
		return s.layerExImageNative(vm, context, strings.TrimPrefix(operation, "Image."), args, budget)
	}
	if strings.HasPrefix(operation, "Font.") {
		return s.layerExFontNative(vm, context, strings.TrimPrefix(operation, "Font."), args, budget)
	}
	if strings.HasPrefix(operation, "Appearance.") {
		return s.layerExAppearanceNative(vm, context, strings.TrimPrefix(operation, "Appearance."), args, budget)
	}
	if strings.HasPrefix(operation, "Path.") {
		return s.layerExPathNative(vm, context, strings.TrimPrefix(operation, "Path."), args, budget)
	}
	return tjs.VoidValue, fmt.Errorf("unknown GdiPlus operation %q", operation)
}

func (s *Services) layerExPointNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	id, err := layerExObjectID(context, "GdiPlus.PointF."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if operation == "@initialize" || operation == "PointF" {
		if len(args) < 2 {
			if operation == "@initialize" {
				if names, namesErr := vm.ClassNames(context); namesErr == nil && len(names) > 1 {
					layerExStoreObject(s, id, "PointF", layerExPoint{})
					return tjs.VoidValue, nil
				}
			}
			return tjs.VoidValue, fmt.Errorf("GdiPlus.PointF requires two arguments")
		}
		point := layerExPoint{}
		point.x, err = layerExFloat(args[0], "GdiPlus.PointF.x")
		if err == nil {
			point.y, err = layerExFloat(args[1], "GdiPlus.PointF.y")
		}
		if err == nil {
			layerExStoreObject(s, id, "PointF", point)
		}
		return tjs.VoidValue, err
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.LayerExObjects, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	state := s.LayerExObjects[id]
	s.mu.RUnlock()
	if state == nil || state.kind != "PointF" {
		return tjs.VoidValue, fmt.Errorf("GdiPlus.PointF object is unavailable")
	}
	point := state.value.(layerExPoint)
	switch operation {
	case "get:x":
		return tjs.RealValue(point.x), nil
	case "get:y":
		return tjs.RealValue(point.y), nil
	case "set:x", "set:y":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.PointF.%s requires one value", operation)
		}
		value, err := layerExFloat(args[0], "GdiPlus.PointF."+operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		if operation == "set:x" {
			point.x = value
		} else {
			point.y = value
		}
		layerExStoreObject(s, id, "PointF", point)
		return tjs.VoidValue, nil
	case "Equals":
		if len(args) != 1 {
			return layerExBooleanValue(false), nil
		}
		other, err := layerExPointFromValue(vm, s, args[0], budget)
		if err != nil {
			return layerExBooleanValue(false), nil
		}
		s.mu.RLock()
		if current := s.LayerExObjects[id]; current != nil {
			if currentPoint, valid := current.value.(layerExPoint); valid {
				point = currentPoint
			}
		}
		s.mu.RUnlock()
		return layerExBooleanValue(point == other), nil
	}
	return tjs.VoidValue, fmt.Errorf("unknown GdiPlus.PointF operation %q", operation)
}

func (s *Services) layerExRectNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	id, err := layerExObjectID(context, "GdiPlus.RectF."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if operation == "@initialize" || operation == "RectF" {
		if len(args) != 4 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.RectF requires four arguments")
		}
		rect := layerExRect{}
		rect.x, err = layerExFloat(args[0], "GdiPlus.RectF.x")
		if err == nil {
			rect.y, err = layerExFloat(args[1], "GdiPlus.RectF.y")
		}
		if err == nil {
			rect.w, err = layerExFloat(args[2], "GdiPlus.RectF.width")
		}
		if err == nil {
			rect.h, err = layerExFloat(args[3], "GdiPlus.RectF.height")
		}
		if err == nil {
			layerExStoreObject(s, id, "RectF", rect)
		}
		return tjs.VoidValue, err
	}
	if operation == "UnionStatic" {
		if len(args) != 3 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.RectF.Union requires three rectangles")
		}
		first, err := layerExRectFromValue(vm, s, args[1], budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		second, err := layerExRectFromValue(vm, s, args[2], budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		return layerExBooleanValue((first.w > 0 && first.h > 0) || (second.w > 0 && second.h > 0)), nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.LayerExObjects, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	state := s.LayerExObjects[id]
	s.mu.RUnlock()
	if state == nil || state.kind != "RectF" {
		return tjs.VoidValue, fmt.Errorf("GdiPlus.RectF object is unavailable")
	}
	rect := state.value.(layerExRect)
	if strings.HasPrefix(operation, "get:") {
		switch strings.TrimPrefix(operation, "get:") {
		case "x", "left":
			return tjs.RealValue(rect.x), nil
		case "y", "top":
			return tjs.RealValue(rect.y), nil
		case "width":
			return tjs.RealValue(rect.w), nil
		case "height":
			return tjs.RealValue(rect.h), nil
		case "right":
			return tjs.RealValue(rect.x + rect.w), nil
		case "bottom":
			return tjs.RealValue(rect.y + rect.h), nil
		case "location":
			return layerExPointValue(vm, s, layerExPoint{rect.x, rect.y}, budget)
		case "bounds":
			return layerExRectValue(vm, s, rect, budget)
		}
	}
	if strings.HasPrefix(operation, "set:") {
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.RectF.%s requires one value", operation)
		}
		value, err := layerExFloat(args[0], "GdiPlus.RectF."+operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		switch strings.TrimPrefix(operation, "set:") {
		case "x":
			rect.x = value
		case "y":
			rect.y = value
		case "width":
			rect.w = value
		case "height":
			rect.h = value
		default:
			return tjs.VoidValue, fmt.Errorf("unknown GdiPlus.RectF property %q", operation)
		}
		layerExStoreObject(s, id, "RectF", rect)
		return tjs.VoidValue, nil
	}
	switch operation {
	case "Clone":
		return layerExRectValue(vm, s, rect, budget)
	case "Equals":
		if len(args) != 1 {
			return layerExBooleanValue(false), nil
		}
		other, err := layerExRectFromValue(vm, s, args[0], budget)
		if err != nil {
			return layerExBooleanValue(false), nil
		}
		s.mu.RLock()
		if current := s.LayerExObjects[id]; current != nil {
			if currentRect, valid := current.value.(layerExRect); valid {
				rect = currentRect
			}
		}
		s.mu.RUnlock()
		return layerExBooleanValue(rect == other), nil
	case "Inflate":
		if len(args) != 2 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.RectF.Inflate requires two values")
		}
		dx, err := layerExFloat(args[0], "GdiPlus.RectF.Inflate")
		if err != nil {
			return tjs.VoidValue, err
		}
		dy, err := layerExFloat(args[1], "GdiPlus.RectF.Inflate")
		if err != nil {
			return tjs.VoidValue, err
		}
		rect.x, rect.y, rect.w, rect.h = rect.x-dx, rect.y-dy, rect.w+2*dx, rect.h+2*dy
		layerExStoreObject(s, id, "RectF", rect)
	case "InflatePoint":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.RectF.InflatePoint requires a PointF")
		}
		point, err := layerExPointFromValue(vm, s, args[0], budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.RLock()
		if current := s.LayerExObjects[id]; current != nil {
			if currentRect, valid := current.value.(layerExRect); valid {
				rect = currentRect
			}
		}
		s.mu.RUnlock()
		rect.x, rect.y, rect.w, rect.h = rect.x-point.x, rect.y-point.y, rect.w+2*point.x, rect.h+2*point.y
		layerExStoreObject(s, id, "RectF", rect)
	case "Offset":
		if len(args) != 2 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.RectF.Offset requires two values")
		}
		dx, err := layerExFloat(args[0], "GdiPlus.RectF.Offset")
		if err != nil {
			return tjs.VoidValue, err
		}
		dy, err := layerExFloat(args[1], "GdiPlus.RectF.Offset")
		if err != nil {
			return tjs.VoidValue, err
		}
		rect.x, rect.y = rect.x+dx, rect.y+dy
		layerExStoreObject(s, id, "RectF", rect)
	case "IntersectsWith":
		if len(args) != 1 {
			return layerExBooleanValue(false), nil
		}
		other, err := layerExRectFromValue(vm, s, args[0], budget)
		if err != nil {
			return layerExBooleanValue(false), nil
		}
		return layerExBooleanValue(rect.x < other.x+other.w && rect.x+rect.w > other.x && rect.y < other.y+other.h && rect.y+rect.h > other.y), nil
	case "IsEmptyArea":
		return layerExBooleanValue(rect.w <= 0 || rect.h <= 0), nil
	case "Union":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.RectF.Union requires a RectF")
		}
		other, err := layerExRectFromValue(vm, s, args[0], budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		layerExStoreObject(s, id, "RectF", layerExUnionRect(rect, other))
	}
	return tjs.VoidValue, nil
}
func (s *Services) layerExMatrixNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	id, err := layerExObjectID(context, "GdiPlus.Matrix."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if operation == "@initialize" || operation == "Matrix" {
		matrix := layerExIdentity()
		switch len(args) {
		case 0:
		case 2:
			rect, rectErr := layerExRectFromValue(vm, s, args[0], budget)
			if rectErr != nil {
				return tjs.VoidValue, rectErr
			}
			point, pointErr := layerExPointFromValue(vm, s, args[1], budget)
			if pointErr != nil {
				return tjs.VoidValue, pointErr
			}
			matrix = layerExAffineValue(rect.w, 0, 0, rect.h, point.x, point.y)
		case 6:
			values := make([]float64, 6)
			for i := range values {
				values[i], err = layerExFloat(args[i], "GdiPlus.Matrix")
				if err != nil {
					return tjs.VoidValue, err
				}
			}
			matrix = layerExAffineValue(values[0], values[1], values[2], values[3], values[4], values[5])
		default:
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Matrix requires zero, two, or six arguments")
		}
		layerExStoreObject(s, id, "Matrix", layerExMatrixState{matrix: matrix})
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.LayerExObjects, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	state := s.LayerExObjects[id]
	s.mu.RUnlock()
	if state == nil || state.kind != "Matrix" {
		return tjs.VoidValue, fmt.Errorf("GdiPlus.Matrix object is unavailable")
	}
	matrixState, ok := state.value.(layerExMatrixState)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("GdiPlus.Matrix state is invalid")
	}
	matrix := matrixState.matrix
	status := matrixState.status
	if strings.HasPrefix(operation, "get:") {
		switch strings.TrimPrefix(operation, "get:") {
		case "m11":
			return tjs.RealValue(matrix.a), nil
		case "m12":
			return tjs.RealValue(matrix.b), nil
		case "m21":
			return tjs.RealValue(matrix.c), nil
		case "m22":
			return tjs.RealValue(matrix.d), nil
		case "dx":
			return tjs.RealValue(matrix.tx), nil
		case "dy":
			return tjs.RealValue(matrix.ty), nil
		}
	}
	switch operation {
	case "OffsetX":
		return tjs.RealValue(matrix.tx), nil
	case "OffsetY":
		return tjs.RealValue(matrix.ty), nil
	case "Equals":
		if len(args) != 1 {
			return layerExBooleanValue(false), nil
		}
		other, err := s.layerExMatrixFromValueWithVM(vm, args[0], budget)
		return layerExBooleanValue(err == nil && matrix == other), nil
	case "SetElements", "SetElemetns":
		if len(args) != 6 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Matrix.%s requires six values", operation)
		}
		values := make([]float64, 6)
		for i := range values {
			values[i], err = layerExFloat(args[i], "GdiPlus.Matrix."+operation)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		matrix = layerExAffineValue(values[0], values[1], values[2], values[3], values[4], values[5])
		status = 0
	case "GetLastStatus":
		result := status
		status = 0
		layerExStoreObject(s, id, "Matrix", layerExMatrixState{matrix: matrix, status: status})
		return tjs.IntegerValue(result), nil
	case "Invert":
		var invertible bool
		matrix, invertible = matrix.invert()
		if !invertible {
			status = 2
			layerExStoreObject(s, id, "Matrix", layerExMatrixState{matrix: matrixState.matrix, status: status})
			return tjs.IntegerValue(status), nil
		}
		status = 0
	case "IsIdentity":
		return layerExBooleanValue(matrix == layerExIdentity()), nil
	case "IsInvertible":
		_, ok := matrix.invert()
		return layerExBooleanValue(ok), nil
	case "Multiply":
		if len(args) != 2 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Matrix.Multiply requires a Matrix and order")
		}
		other, err := s.layerExMatrixFromValueWithVM(vm, args[0], budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		order, err := layerExInt(args[1], "GdiPlus.Matrix.Multiply order")
		if err != nil {
			return tjs.VoidValue, err
		}
		if layerExMatrixInvalidOrder(order) {
			status = 2
			layerExStoreObject(s, id, "Matrix", layerExMatrixState{matrix: matrix, status: status})
			return tjs.IntegerValue(status), nil
		}
		s.mu.RLock()
		if current := s.LayerExObjects[id]; current != nil {
			if currentState, valid := current.value.(layerExMatrixState); valid {
				matrix = currentState.matrix
			}
		}
		s.mu.RUnlock()
		if order == 0 {
			matrix = matrix.multiply(other)
		} else {
			matrix = other.multiply(matrix)
		}
		status = 0
	case "Reset":
		matrix = layerExIdentity()
		status = 0
	case "Rotate":
		if len(args) != 2 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Matrix.Rotate requires angle and order")
		}
		angle, err := layerExFloat(args[0], "GdiPlus.Matrix.Rotate")
		if err != nil {
			return tjs.VoidValue, err
		}
		order, err := layerExInt(args[1], "GdiPlus.Matrix.Rotate order")
		if err != nil {
			return tjs.VoidValue, err
		}
		if layerExMatrixInvalidOrder(order) {
			status = 2
			layerExStoreObject(s, id, "Matrix", layerExMatrixState{matrix: matrix, status: status})
			return tjs.IntegerValue(status), nil
		}
		rotation := layerExRotation(angle)
		if order == 0 {
			matrix = matrix.multiply(rotation)
		} else {
			matrix = rotation.multiply(matrix)
		}
		status = 0
	case "RotateAt":
		if len(args) != 3 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Matrix.RotateAt requires angle, PointF, and order")
		}
		angle, err := layerExFloat(args[0], "GdiPlus.Matrix.RotateAt")
		if err != nil {
			return tjs.VoidValue, err
		}
		point, err := layerExPointFromValue(vm, s, args[1], budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		order, err := layerExInt(args[2], "GdiPlus.Matrix.RotateAt order")
		if err != nil {
			return tjs.VoidValue, err
		}
		if layerExMatrixInvalidOrder(order) {
			status = 2
			layerExStoreObject(s, id, "Matrix", layerExMatrixState{matrix: matrix, status: status})
			return tjs.IntegerValue(status), nil
		}
		rotation := layerExTranslation(point.x, point.y).multiply(layerExRotation(angle)).multiply(layerExTranslation(-point.x, -point.y))
		if order == 0 {
			matrix = matrix.multiply(rotation)
		} else {
			matrix = rotation.multiply(matrix)
		}
		status = 0
	case "Scale":
		if len(args) != 3 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Matrix.Scale requires two values and order")
		}
		sx, err := layerExFloat(args[0], "GdiPlus.Matrix.Scale")
		if err != nil {
			return tjs.VoidValue, err
		}
		sy, err := layerExFloat(args[1], "GdiPlus.Matrix.Scale")
		if err != nil {
			return tjs.VoidValue, err
		}
		order, err := layerExInt(args[2], "GdiPlus.Matrix.Scale order")
		if err != nil {
			return tjs.VoidValue, err
		}
		if layerExMatrixInvalidOrder(order) {
			status = 2
			layerExStoreObject(s, id, "Matrix", layerExMatrixState{matrix: matrix, status: status})
			return tjs.IntegerValue(status), nil
		}
		scale := layerExScale(sx, sy)
		if order == 0 {
			matrix = matrix.multiply(scale)
		} else {
			matrix = scale.multiply(matrix)
		}
		status = 0
	case "Shear":
		if len(args) != 3 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Matrix.Shear requires two values and order")
		}
		shx, err := layerExFloat(args[0], "GdiPlus.Matrix.Shear")
		if err != nil {
			return tjs.VoidValue, err
		}
		shy, err := layerExFloat(args[1], "GdiPlus.Matrix.Shear")
		if err != nil {
			return tjs.VoidValue, err
		}
		order, err := layerExInt(args[2], "GdiPlus.Matrix.Shear order")
		if err != nil {
			return tjs.VoidValue, err
		}
		if layerExMatrixInvalidOrder(order) {
			status = 2
			layerExStoreObject(s, id, "Matrix", layerExMatrixState{matrix: matrix, status: status})
			return tjs.IntegerValue(status), nil
		}
		shear := layerExAffineValue(1, shy, shx, 1, 0, 0)
		if order == 0 {
			matrix = matrix.multiply(shear)
		} else {
			matrix = shear.multiply(matrix)
		}
		status = 0
	case "Translate":
		if len(args) != 3 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Matrix.Translate requires two values and order")
		}
		tx, err := layerExFloat(args[0], "GdiPlus.Matrix.Translate")
		if err != nil {
			return tjs.VoidValue, err
		}
		ty, err := layerExFloat(args[1], "GdiPlus.Matrix.Translate")
		if err != nil {
			return tjs.VoidValue, err
		}
		order, err := layerExInt(args[2], "GdiPlus.Matrix.Translate order")
		if err != nil {
			return tjs.VoidValue, err
		}
		if layerExMatrixInvalidOrder(order) {
			status = 2
			layerExStoreObject(s, id, "Matrix", layerExMatrixState{matrix: matrix, status: status})
			return tjs.IntegerValue(status), nil
		}
		translation := layerExTranslation(tx, ty)
		if order == 0 {
			matrix = matrix.multiply(translation)
		} else {
			matrix = translation.multiply(matrix)
		}
		status = 0
	default:
		return tjs.VoidValue, fmt.Errorf("unknown GdiPlus.Matrix operation %q", operation)
	}
	layerExStoreObject(s, id, "Matrix", layerExMatrixState{matrix: matrix, status: status})
	return tjs.IntegerValue(status), nil
}

func (s *Services) layerExMatrixFromValueWithVM(vm *tjs.VM, value tjs.Value, budget *uint64) (layerExAffine, error) {
	if value.Kind() != tjs.KindObject || value.IsNull() {
		return layerExAffine{}, fmt.Errorf("Matrix value requires an object")
	}
	s.ensureInit()
	s.mu.RLock()
	state := s.LayerExObjects[value.ObjectRef().Object]
	s.mu.RUnlock()
	if state != nil && state.kind == "Matrix" {
		matrixState, ok := state.value.(layerExMatrixState)
		if !ok {
			return layerExAffine{}, fmt.Errorf("Matrix state is invalid")
		}
		return matrixState.matrix, nil
	}
	names := [...]string{"m11", "m12", "m21", "m22", "dx", "dy"}
	indices := [...]string{"0", "1", "2", "3", "4", "5"}
	defaults := [6]float64{1, 0, 0, 1, 0, 0}
	values := defaults
	order := [...]int{3, 2, 1, 0, 5, 4}
	for _, i := range order {
		var member tjs.Value
		var found bool
		for pass := 0; pass < 2; pass++ {
			var err error
			member, found, err = layerExMember(vm, s, value, names[i], budget)
			if err != nil {
				return layerExAffine{}, err
			}
		}
		if !found {
			values[i] = layerExFloatMember(vm, s, value, indices[i], values[i], budget)
			continue
		}
		parsed, parseErr := layerExFloat(member, "Matrix element")
		if parseErr != nil {
			values[i] = defaults[i]
		} else {
			values[i] = parsed
		}
	}
	return layerExAffineValue(values[0], values[1], values[2], values[3], values[4], values[5]), nil
}

func (s *Services) layerExImageNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	id, err := layerExObjectID(context, "GdiPlus.Image."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if operation == "@initialize" || operation == "Image" {
		state := &layerExImage{}
		if len(args) > 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Image accepts zero or one argument")
		}
		if len(args) == 1 && args[0].Kind() != tjs.KindVoid {
			imageData, imageErr := layerExImageFromValue(s, args[0], budget)
			if imageErr != nil {
				if args[0].Kind() != tjs.KindString {
					return tjs.VoidValue, imageErr
				}
				return tjs.VoidValue, imageErr
			}
			state.image, state.valid = layerExNormalizeImage(imageData), true
		}
		layerExStoreObject(s, id, "Image", state)
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.LayerExObjects, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	stateValue := s.LayerExObjects[id]
	s.mu.RUnlock()
	if stateValue == nil || stateValue.kind != "Image" {
		return tjs.VoidValue, fmt.Errorf("GdiPlus.Image object is unavailable")
	}
	state := stateValue.value.(*layerExImage)
	switch operation {
	case "load":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Image.load requires a storage name")
		}
		data, err := s.readStorage(args[0].Text())
		if err != nil {
			return tjs.VoidValue, err
		}
		imageData, err := layerExDecodeImage(data)
		if err != nil {
			return tjs.VoidValue, err
		}
		state.image, state.valid = imageData, true
	case "Clone":
		value, err := layerExNewObject(vm, s, "GdiPlus.Image", nil, budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		copyState := &layerExImage{image: layerExNormalizeImage(state.image), valid: state.valid}
		layerExStoreObject(s, value.ObjectRef().Object, "Image", copyState)
		return value, nil
	case "GetBounds":
		if !state.valid {
			return layerExRectValue(vm, s, layerExRect{}, budget)
		}
		return layerExRectValue(vm, s, layerExRect{w: float64(state.image.Width), h: float64(state.image.Height)}, budget)
	case "GetWidth":
		return tjs.IntegerValue(int64(state.image.Width)), nil
	case "GetHeight":
		return tjs.IntegerValue(int64(state.image.Height)), nil
	case "GetHorizontalResolution", "GetVerticalResolution":
		return tjs.RealValue(96), nil
	case "GetFlags":
		return tjs.IntegerValue(0), nil
	case "GetLastStatus":
		return tjs.IntegerValue(0), nil
	case "GetPixelFormat":
		return tjs.IntegerValue(0x26200a), nil
	case "GetType":
		return tjs.IntegerValue(1), nil
	case "RotateFlip":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Image.RotateFlip requires a mode")
		}
		mode, err := layerExInt(args[0], "GdiPlus.Image.RotateFlip")
		if err != nil {
			return tjs.VoidValue, err
		}
		state.image = layerExRotateFlip(state.image, mode)
	default:
		return tjs.VoidValue, fmt.Errorf("unknown GdiPlus.Image operation %q", operation)
	}
	return tjs.VoidValue, nil
}
func (s *Services) layerExFontNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	id, err := layerExObjectID(context, "GdiPlus.Font."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if operation == "@initialize" || operation == "Font" {
		state := &layerExFont{familyName: "", emSize: 12}
		if len(args) != 3 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Font requires family name, size, and style")
		}
		state.familyName = args[0].Text()
		state.emSize, err = layerExFloat(args[1], "GdiPlus.Font.emSize")
		if err == nil {
			state.style, err = layerExInt(args[2], "GdiPlus.Font.style")
		}
		if err == nil && state.emSize <= 0 {
			err = fmt.Errorf("GdiPlus.Font.emSize must be positive")
		}
		if err == nil {
			layerExStoreObject(s, id, "Font", state)
		}
		return tjs.VoidValue, err
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.LayerExObjects, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	stateValue := s.LayerExObjects[id]
	s.mu.RUnlock()
	if stateValue == nil || stateValue.kind != "Font" {
		return tjs.VoidValue, fmt.Errorf("GdiPlus.Font object is unavailable")
	}
	state := stateValue.value.(*layerExFont)
	if strings.HasPrefix(operation, "get:") {
		switch strings.TrimPrefix(operation, "get:") {
		case "familyName":
			return tjs.StringValue(state.familyName), nil
		case "emSize":
			return tjs.RealValue(state.emSize), nil
		case "style":
			return tjs.IntegerValue(state.style), nil
		case "forceSelfPathDraw":
			return layerExBooleanValue(state.forceSelfPath), nil
		case "ascent", "descent", "ascentLeading", "descentLeading", "lineSpacing":
			face, err := s.fontFace(&FontState{Face: state.familyName, Height: -int32(math.Round(state.emSize)), Bold: state.style&1 != 0, Italic: state.style&2 != 0})
			if err != nil {
				return tjs.RealValue(0), nil
			}
			metrics, metricsErr := MetricsForFace(face)
			face.Close()
			if metricsErr != nil {
				return tjs.RealValue(0), nil
			}
			leading := float64(metrics.Height - metrics.Ascent - metrics.Descent)
			switch strings.TrimPrefix(operation, "get:") {
			case "ascent":
				return tjs.RealValue(float64(metrics.Ascent)), nil
			case "descent":
				return tjs.RealValue(float64(metrics.Descent)), nil
			case "ascentLeading":
				return tjs.RealValue(leading / 2), nil
			case "descentLeading":
				return tjs.RealValue(-leading / 2), nil
			case "lineSpacing":
				return tjs.RealValue(float64(metrics.Height)), nil
			}
		}
	}
	if strings.HasPrefix(operation, "set:") {
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Font.%s requires one value", operation)
		}
		switch strings.TrimPrefix(operation, "set:") {
		case "familyName":
			state.familyName = args[0].Text()
		case "emSize":
			state.emSize, err = layerExFloat(args[0], "GdiPlus.Font.emSize")
		case "style":
			state.style, err = layerExInt(args[0], "GdiPlus.Font.style")
		case "forceSelfPathDraw":
			state.forceSelfPath, err = layerExBool(args[0], "GdiPlus.Font.forceSelfPathDraw")
		default:
			return tjs.VoidValue, fmt.Errorf("unknown GdiPlus.Font property %q", operation)
		}
		return tjs.VoidValue, err
	}
	return tjs.VoidValue, fmt.Errorf("unknown GdiPlus.Font operation %q", operation)
}

func (s *Services) layerExAppearanceNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	id, err := layerExObjectID(context, "GdiPlus.Appearance."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if operation == "@initialize" || operation == "Appearance" {
		if len(args) != 0 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Appearance takes no arguments")
		}
		layerExStoreObject(s, id, "Appearance", &layerExAppearance{})
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.LayerExObjects, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	stateValue := s.LayerExObjects[id]
	s.mu.RUnlock()
	if stateValue == nil || stateValue.kind != "Appearance" {
		return tjs.VoidValue, fmt.Errorf("GdiPlus.Appearance object is unavailable")
	}
	state := stateValue.value.(*layerExAppearance)
	switch operation {
	case "clear":
		state.paints = nil
	case "addBrush":
		if len(args) < 1 || len(args) > 3 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Appearance.addBrush requires color and optional offsets")
		}
		paint, err := layerExPaintFromValue(vm, s, args[0], layerExBrush, tjs.VoidValue, budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		if len(args) > 1 {
			paint.offset.x, err = layerExFloat(args[1], "GdiPlus.Appearance.addBrush offset")
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		if len(args) > 2 {
			paint.offset.y, err = layerExFloat(args[2], "GdiPlus.Appearance.addBrush offset")
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		state.paints = append(state.paints, paint)
	case "addPen":
		if len(args) < 2 || len(args) > 4 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Appearance.addPen requires color and width")
		}
		paint, err := layerExPaintFromValue(vm, s, args[0], layerExPen, args[1], budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		if len(args) > 2 {
			paint.offset.x, err = layerExFloat(args[2], "GdiPlus.Appearance.addPen offset")
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		if len(args) > 3 {
			paint.offset.y, err = layerExFloat(args[3], "GdiPlus.Appearance.addPen offset")
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		state.paints = append(state.paints, paint)
	default:
		return tjs.VoidValue, fmt.Errorf("unknown GdiPlus.Appearance operation %q", operation)
	}
	return tjs.VoidValue, nil
}

func (s *Services) layerExPathNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	id, err := layerExObjectID(context, "GdiPlus.Path."+operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	if operation == "@initialize" || operation == "Path" {
		if len(args) != 0 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Path takes no arguments")
		}
		layerExStoreObject(s, id, "Path", &layerExPath{})
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.LayerExObjects, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	stateValue := s.LayerExObjects[id]
	s.mu.RUnlock()
	if stateValue == nil || stateValue.kind != "Path" {
		return tjs.VoidValue, fmt.Errorf("GdiPlus.Path object is unavailable")
	}
	path := stateValue.value.(*layerExPath)
	switch operation {
	case "startFigure":
		return tjs.VoidValue, nil
	case "closeFigure":
		if len(path.subpaths) > 0 {
			path.subpaths[len(path.subpaths)-1].closed = true
		}
		return tjs.VoidValue, nil
	case "drawArc", "drawPie":
		if len(args) != 6 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Path.%s requires six values", operation)
		}
		values := make([]float64, 6)
		for i := range values {
			values[i], err = layerExFloat(args[i], "GdiPlus.Path."+operation)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		layerExPathArc(path, values[0], values[1], values[2], values[3], values[4], values[5], operation == "drawPie")
	case "drawBezier":
		if len(args) != 8 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Path.drawBezier requires eight values")
		}
		values := make([]float64, 8)
		for i := range values {
			values[i], err = layerExFloat(args[i], "GdiPlus.Path.drawBezier")
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		layerExPathAddPolyline(path, layerExBezierPoints(layerExPoint{values[0], values[1]}, layerExPoint{values[2], values[3]}, layerExPoint{values[4], values[5]}, layerExPoint{values[6], values[7]}, 32), false)
	case "drawBeziers", "drawLines", "drawPolygon":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Path.%s requires a point list", operation)
		}
		points, err := layerExPointsFromValue(vm, s, args[0], budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		if operation == "drawBeziers" && len(points) >= 4 {
			out := make([]layerExPoint, 0)
			for i := 0; i+3 < len(points); i += 3 {
				segment := layerExBezierPoints(points[i], points[i+1], points[i+2], points[i+3], 24)
				if i > 0 {
					segment = segment[1:]
				}
				out = append(out, segment...)
			}
			layerExPathAddPolyline(path, out, false)
		} else {
			layerExPathAddPolyline(path, points, operation == "drawPolygon")
		}
	case "drawClosedCurve", "drawClosedCurve2", "drawCurve", "drawCurve2", "drawCurve3":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Path.%s requires a point list", operation)
		}
		points, err := layerExPointsFromValue(vm, s, args[0], budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		tension := 0.5
		if operation == "drawClosedCurve2" || operation == "drawCurve2" {
			if len(args) != 2 {
				return tjs.VoidValue, fmt.Errorf("GdiPlus.Path.%s requires tension", operation)
			}
			tension, err = layerExFloat(args[1], "GdiPlus.Path."+operation)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		layerExPathAddPolyline(path, layerExCardinalPoints(points, strings.HasPrefix(operation, "drawClosed"), tension), strings.HasPrefix(operation, "drawClosed"))
	case "drawEllipse", "drawRectangle":
		if len(args) != 4 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Path.%s requires four values", operation)
		}
		values := make([]float64, 4)
		for i := range values {
			values[i], err = layerExFloat(args[i], "GdiPlus.Path."+operation)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		if operation == "drawEllipse" {
			layerExPathAddPolyline(path, layerExArcPoints(values[0], values[1], values[2], values[3], 0, 360), true)
		} else {
			layerExPathAddPolyline(path, []layerExPoint{{values[0], values[1]}, {values[0] + values[2], values[1]}, {values[0] + values[2], values[1] + values[3]}, {values[0], values[1] + values[3]}}, true)
		}
	case "drawRectangles":
		if len(args) != 1 {
			return tjs.VoidValue, fmt.Errorf("GdiPlus.Path.drawRectangles requires a rectangle list")
		}
		rects, err := layerExRectsFromValue(vm, s, args[0], budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		for _, rect := range rects {
			layerExPathAddPolyline(path, []layerExPoint{{rect.x, rect.y}, {rect.x + rect.w, rect.y}, {rect.x + rect.w, rect.y + rect.h}, {rect.x, rect.y + rect.h}}, true)
		}
	default:
		return tjs.VoidValue, fmt.Errorf("unknown GdiPlus.Path operation %q", operation)
	}
	return tjs.VoidValue, nil
}
func layerExRegisterNativeClass(vm *tjs.VM, root tjs.Value, name string, methods []string, properties map[string]bool) error {
	return layerExRegisterNativeClassNamed(vm, root, name, name, methods, properties)
}

func layerExRegisterNativeClassNamed(vm *tjs.VM, root tjs.Value, nativeName, scriptName string, methods []string, properties map[string]bool) error {
	class, err := vm.NewNativeClass(nativeName, "GdiPlus."+scriptName+".@initialize")
	if err != nil {
		return err
	}
	if nativeName != scriptName {
		if err := vm.AddNativeClassAlias(class, scriptName); err != nil {
			return err
		}
	}
	allMethods := make([]string, 0, len(methods)+2)
	allMethods = append(allMethods, scriptName, "finalize")
	allMethods = append(allMethods, methods...)
	for _, method := range allMethods {
		if err := vm.RegisterNativeMethod(class, method, "GdiPlus."+scriptName+"."+method); err != nil {
			return err
		}
	}
	for property, writable := range properties {
		setter := ""
		if writable {
			setter = "GdiPlus." + scriptName + ".set:" + property
		}
		if err := vm.RegisterNativeProperty(class, property, "GdiPlus."+scriptName+".get:"+property, setter); err != nil {
			return err
		}
	}
	return vm.RegisterNativeStaticValue(root, scriptName, class)
}

func layerExGdiPlusConstants() map[string]int64 {
	return map[string]int64{
		"Ok": 0, "GenericError": 1, "InvalidParameter": 2, "OutOfMemory": 3, "ObjectBusy": 4,
		"InsufficientBuffer": 5, "NotImplemented": 6, "Win32Error": 7, "WrongState": 8, "Aborted": 9,
		"FileNotFound": 10, "ValueOverflow": 11, "AccessDenied": 12, "UnknownImageFormat": 13,
		"FontFamilyNotFound": 14, "FontStyleNotFound": 15, "NotTrueTypeFont": 16,
		"UnsupportedGdiplusVersion": 17, "GdiplusNotInitialized": 18, "PropertyNotFound": 19,
		"PropertyNotSupported": 20, "ProfileNotFound": 21,
		"FontStyleRegular": 0, "FontStyleBold": 1, "FontStyleItalic": 2, "FontStyleBoldItalic": 3,
		"FontStyleUnderline": 4, "FontStyleStrikeout": 8,
		"BrushTypeSolidColor": 0, "BrushTypeHatchFill": 1, "BrushTypeTextureFill": 2,
		"BrushTypePathGradient": 3, "BrushTypeLinearGradient": 4,
		"DashCapFlat": 0, "DashCapRound": 2, "DashCapTriangle": 3,
		"DashStyleSolid": 0, "DashStyleDash": 1, "DashStyleDot": 2, "DashStyleDashDot": 3, "DashStyleDashDotDot": 4,
		"HatchStyleHorizontal": 0, "HatchStyleVertical": 1, "HatchStyleForwardDiagonal": 2, "HatchStyleBackwardDiagonal": 3,
		"HatchStyleCross": 4, "HatchStyleDiagonalCross": 5, "HatchStyle05Percent": 6, "HatchStyle10Percent": 7,
		"HatchStyle20Percent": 8, "HatchStyle25Percent": 9, "HatchStyle30Percent": 10, "HatchStyle40Percent": 11,
		"HatchStyle50Percent": 12, "HatchStyle60Percent": 13, "HatchStyle70Percent": 14, "HatchStyle75Percent": 15,
		"HatchStyle80Percent": 16, "HatchStyle90Percent": 17, "HatchStyleLightDownwardDiagonal": 18, "HatchStyleLightUpwardDiagonal": 19,
		"HatchStyleDarkDownwardDiagonal": 20, "HatchStyleDarkUpwardDiagonal": 21, "HatchStyleWideDownwardDiagonal": 22,
		"HatchStyleWideUpwardDiagonal": 23, "HatchStyleLightVertical": 24, "HatchStyleLightHorizontal": 25,
		"HatchStyleNarrowVertical": 26, "HatchStyleNarrowHorizontal": 27, "HatchStyleDarkVertical": 28,
		"HatchStyleDarkHorizontal": 29, "HatchStyleDashedDownwardDiagonal": 30, "HatchStyleDashedUpwardDiagonal": 31,
		"HatchStyleDashedHorizontal": 32, "HatchStyleDashedVertical": 33, "HatchStyleSmallConfetti": 34,
		"HatchStyleLargeConfetti": 35, "HatchStyleZigZag": 36, "HatchStyleWave": 37, "HatchStyleDiagonalBrick": 38,
		"HatchStyleHorizontalBrick": 39, "HatchStyleWeave": 40, "HatchStylePlaid": 41, "HatchStyleDivot": 42,
		"HatchStyleDottedGrid": 43, "HatchStyleDottedDiamond": 44, "HatchStyleShingle": 45, "HatchStyleTrellis": 46,
		"HatchStyleSphere": 47, "HatchStyleSmallGrid": 48, "HatchStyleSmallCheckerBoard": 49, "HatchStyleLargeCheckerBoard": 50,
		"HatchStyleOutlinedDiamond": 51, "HatchStyleSolidDiamond": 52, "HatchStyleTotal": 53,
		"HatchStyleLargeGrid": 4, "HatchStyleMin": 0, "HatchStyleMax": 52,
		"LinearGradientModeHorizontal": 0, "LinearGradientModeVertical": 1,
		"LinearGradientModeForwardDiagonal": 2, "LinearGradientModeBackwardDiagonal": 3,
		"LineCapFlat": 0, "LineCapSquare": 1, "LineCapRound": 2, "LineCapTriangle": 3,
		"LineCapNoAnchor": 16, "LineCapSquareAnchor": 17, "LineCapRoundAnchor": 18,
		"LineCapDiamondAnchor": 19, "LineCapArrowAnchor": 20,
		"LineJoinMiter": 0, "LineJoinBevel": 1, "LineJoinRound": 2, "LineJoinMiterClipped": 3,
		"PenAlignmentCenter": 0, "PenAlignmentInset": 1,
		"WrapModeTile": 0, "WrapModeTileFlipX": 1, "WrapModeTileFlipY": 2, "WrapModeTileFlipXY": 3, "WrapModeClamp": 4,
		"MatrixOrderPrepend": 0, "MatrixOrderAppend": 1,
		"ImageTypeUnknown": 0, "ImageTypeBitmap": 1, "ImageTypeMetafile": 2,
		"RotateNoneFlipNone": 0, "Rotate90FlipNone": 1, "Rotate180FlipNone": 2, "Rotate270FlipNone": 3,
		"RotateNoneFlipX": 4, "Rotate90FlipX": 5, "Rotate180FlipX": 6, "Rotate270FlipX": 7,
		"RotateNoneFlipY": 6, "Rotate90FlipY": 7, "Rotate180FlipY": 4, "Rotate270FlipY": 5,
		"RotateNoneFlipXY": 2, "Rotate90FlipXY": 3, "Rotate180FlipXY": 0, "Rotate270FlipXY": 1,
		"SmoothingModeInvalid": -1, "SmoothingModeDefault": 0, "SmoothingModeHighSpeed": 1,
		"SmoothingModeHighQuality": 2, "SmoothingModeNone": 0, "SmoothingModeAntiAlias": 1,
		"TextRenderingHintSystemDefault": 0, "TextRenderingHintSingleBitPerPixelGridFit": 1,
		"TextRenderingHintSingleBitPerPixel": 2, "TextRenderingHintAntiAliasGridFit": 3,
		"TextRenderingHintAntiAlias": 4, "TextRenderingHintClearTypeGridFit": 5,
		"EncoderValueCompressionLZW": 2, "EncoderValueCompressionCCITT3": 3, "EncoderValueCompressionCCITT4": 4,
		"EncoderValueCompressionRle": 5, "EncoderValueCompressionNone": 6,
		"EncoderValueScanMethodInterlaced": 1, "EncoderValueScanMethodNonInterlaced": 0,
		"EncoderValueVersionGif87": 9, "EncoderValueVersionGif89": 10,
		"EncoderValueRenderProgressive": 0, "EncoderValueRenderNonProgressive": 1,
		"EncoderValueTransformRotate90": 1, "EncoderValueTransformRotate180": 2, "EncoderValueTransformRotate270": 3,
		"EncoderValueTransformFlipHorizontal": 4, "EncoderValueTransformFlipVertical": 5,
	}
}

func installLayerExDrawNative(vm *tjs.VM, s *Services) error {
	if vm == nil || s == nil {
		return fmt.Errorf("layerExDraw.dll requires a VM and Services")
	}
	s.ensureInit()
	s.mu.Lock()
	if s.layerExDrawVMs == nil {
		s.layerExDrawVMs = make(map[*tjs.VM]struct{})
	}
	if _, installed := s.layerExDrawVMs[vm]; installed {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	root, ok := vm.Globals["GdiPlus"]
	if !ok {
		var err error
		root, err = vm.NewNativeClass("GdiPlus", "GdiPlus.@initialize")
		if err != nil {
			return err
		}
	}
	for name, value := range layerExGdiPlusConstants() {
		if err := vm.RegisterNativeStaticValue(root, name, tjs.IntegerValue(value)); err != nil {
			return err
		}
	}
	for _, method := range []string{"addPrivateFont", "getFontList"} {
		if err := vm.RegisterNativeStaticMethod(root, method, "GdiPlus."+method); err != nil {
			return err
		}
	}
	if err := layerExRegisterNativeClass(vm, root, "PointF", []string{"Equals"}, map[string]bool{"x": true, "y": true}); err != nil {
		return err
	}
	if err := layerExRegisterNativeClass(vm, root, "RectF", []string{"Clone", "Equals", "Inflate", "InflatePoint", "IntersectsWith", "IsEmptyArea", "Offset"}, map[string]bool{"x": true, "y": true, "width": true, "height": true, "left": false, "top": false, "right": false, "bottom": false, "location": false, "bounds": false}); err != nil {
		return err
	}
	rectClass, err := vm.GetMember(root, "RectF", s, nil)
	if err != nil {
		return err
	}
	if err := vm.RegisterNativeStaticMethod(rectClass, "Union", "GdiPlus.RectF.UnionStatic"); err != nil {
		return err
	}
	if err := layerExRegisterNativeClass(vm, root, "Matrix", []string{"OffsetX", "OffsetY", "Equals", "SetElements", "SetElemetns", "GetLastStatus", "Invert", "IsIdentity", "IsInvertible", "Multiply", "Reset", "Rotate", "RotateAt", "Scale", "Shear", "Translate"}, map[string]bool{"m11": false, "m12": false, "m21": false, "m22": false, "dx": false, "dy": false}); err != nil {
		return err
	}
	if err := layerExRegisterNativeClass(vm, root, "Image", []string{"load", "Clone", "GetBounds", "GetHeight", "GetHorizontalResolution", "GetLastStatus", "GetFlags", "GetPixelFormat", "GetType", "GetVerticalResolution", "GetWidth", "RotateFlip"}, nil); err != nil {
		return err
	}
	if err := layerExRegisterNativeClassNamed(vm, root, "GdiPlusFont", "Font", []string{}, map[string]bool{"familyName": true, "emSize": true, "style": true, "forceSelfPathDraw": true, "ascent": false, "descent": false, "ascentLeading": false, "descentLeading": false, "lineSpacing": false}); err != nil {
		return err
	}
	if err := layerExRegisterNativeClass(vm, root, "Appearance", []string{"clear", "addBrush", "addPen"}, nil); err != nil {
		return err
	}
	if err := layerExRegisterNativeClass(vm, root, "Path", []string{"startFigure", "closeFigure", "drawArc", "drawPie", "drawBezier", "drawBeziers", "drawClosedCurve", "drawClosedCurve2", "drawCurve", "drawCurve2", "drawCurve3", "drawEllipse", "drawLine", "drawLines", "drawPolygon", "drawRectangle", "drawRectangles"}, nil); err != nil {
		return err
	}
	layer, ok := vm.Globals["Layer"]
	if !ok {
		return fmt.Errorf("layerExDraw.dll requires the Layer class")
	}
	methods := []string{"setViewTransform", "resetViewTransform", "rotateViewTransform", "scaleViewTransform", "translateViewTransform", "setTransform", "resetTransform", "rotateTransform", "scaleTransform", "translateTransform", "clear", "drawPath", "drawArc", "drawPie", "drawBezier", "drawBeziers", "drawClosedCurve", "drawClosedCurve2", "drawCurve", "drawCurve2", "drawCurve3", "drawEllipse", "drawLine", "drawLines", "drawPolygon", "drawRectangle", "drawRectangles", "drawPathString", "drawPathString2", "drawString", "measureString", "measureStringInternal", "measureString2", "measureStringInternal2", "drawImage", "drawImageRect", "drawImageStretch", "drawImageAffine", "redrawRecord", "getRecordImage", "saveRecord", "loadRecord", "setResolution", "saveImage", "getColorRegionRects"}
	for _, method := range methods {
		if err := vm.RegisterNativeMethod(layer, method, "LayerExDraw."+method); err != nil {
			return err
		}
	}
	for property, writable := range map[string]bool{"updateWhenDraw": true, "smoothingMode": true, "textRenderingHint": true, "record": true} {
		setter := ""
		if writable {
			setter = "LayerExDraw.set:" + property
		}
		if err := vm.RegisterNativeProperty(layer, property, "LayerExDraw.get:"+property, setter); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.layerExDrawVMs[vm] = struct{}{}
	s.mu.Unlock()
	return nil
}
