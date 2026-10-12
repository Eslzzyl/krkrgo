package wmv3

import (
	"errors"
	"testing"
)

// wmv3Bits writes fields MSB-first in the VC-1 Simple/Main sequence
// header order (Figure 7-8).
type wmv3Bits struct {
	buf []byte
	n   int
}

func (b *wmv3Bits) add(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if b.n%8 == 0 {
			b.buf = append(b.buf, 0)
		}
		if v>>uint(i)&1 == 1 {
			b.buf[b.n/8] |= 1 << (7 - b.n%8)
		}
		b.n++
	}
}

func (b *wmv3Bits) bit(v bool) {
	var u uint32
	if v {
		u = 1
	}
	b.add(u, 1)
}

// headerFields describes one Simple/Main sequence header for the test
// bit writer.
type headerFields struct {
	profile      uint32 // 0 simple, 1 main
	oldInterlace bool
	sprite       bool
	frmrtq       uint32
	bitrtq       uint32
	loopFilter   bool
	multiRes     bool
	resX8        bool
	resFastTX    bool
	fastUVMC     bool
	extendedMV   bool
	dquant       uint32
	vsTransform  bool
	transtab     bool
	overlap      bool
	resync       bool
	rangered     bool
	maxBFrames   uint32
	quantizer    uint32
	finterp      bool
	resRTM       bool
	// Sprite mode extras, written only when sprite is set.
	spriteW  uint32
	spriteH  uint32
	spriteDC bool
}

func validHeader() headerFields {
	return headerFields{profile: 1, resFastTX: true, fastUVMC: true, resRTM: true}
}

func simpleHeader() headerFields {
	return headerFields{profile: 0, resFastTX: true, fastUVMC: true, resRTM: true}
}

func (h headerFields) bytes() []byte {
	var b wmv3Bits
	b.add(h.profile, 2)
	b.bit(h.oldInterlace)
	b.bit(h.sprite)
	b.add(h.frmrtq, 3)
	b.add(h.bitrtq, 5)
	b.bit(h.loopFilter)
	b.bit(h.resX8)
	b.bit(h.multiRes)
	b.bit(h.resFastTX)
	b.bit(h.fastUVMC)
	b.bit(h.extendedMV)
	b.add(h.dquant, 2)
	b.bit(h.vsTransform)
	b.bit(h.transtab)
	b.bit(h.overlap)
	b.bit(h.resync)
	b.bit(h.rangered)
	b.add(h.maxBFrames, 3)
	b.add(h.quantizer, 2)
	b.bit(h.finterp)
	if h.sprite {
		b.add(h.spriteW, 11)
		b.add(h.spriteH, 11)
		b.add(0, 5)       // reserved frame rate
		b.bit(false)      // res_x8 override
		b.bit(h.spriteDC) // optional sprite DC VLC selection
		b.add(0, 3)       // reserved slice code
	} else {
		b.bit(h.resRTM)
	}
	if !h.resFastTX {
		b.add(0x402f, 16) // reserved field FFmpeg documents as always 0x402F
	}
	return b.buf
}

func profileBits(profile uint32) []byte {
	var b wmv3Bits
	b.add(profile, 2)
	return b.buf
}

func TestParseExtradataMainKnownVector(t *testing.T) {
	// Independently computed bit-exact header: profile=main, frmrtq=5,
	// bitrtq=17, loop filter on, multires on, fastuvmc/extended mv on,
	// dquant=2, vs transform on, overlap/resync/rangered on, max_b=2,
	// quantizer=uniform, finterp on, res_rtm on; plus the trailing
	// version byte real WMV3 extradata carries.
	data := []byte{0x4b, 0x1b, 0xeb, 0xaf, 0x01}
	want := Config{
		Profile:        ProfileMain,
		FrmRTQPostProc: 5,
		BitRTQPostProc: 17,
		LoopFilter:     true,
		MultiRes:       true,
		FastUVMC:       true,
		ExtendedMV:     true,
		DQuant:         2,
		VSTransform:    true,
		Overlap:        true,
		ResyncMarker:   true,
		Rangered:       true,
		MaxBFrames:     2,
		QuantizerMode:  QuantUniform,
		FInterpFlag:    true,
		ResFastTX:      true,
		ResRTMFlag:     true,
	}
	got, err := ParseExtradata(data)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("parsed config:\n got %#v\nwant %#v", got, want)
	}
	// The trailing byte is tolerated but not required; parsing the bare
	// 32-bit header yields the identical config.
	bare, err := ParseExtradata(data[:4])
	if err != nil {
		t.Fatal(err)
	}
	if bare != want {
		t.Fatalf("bare header config:\n got %#v\nwant %#v", bare, want)
	}
}

func TestParseExtradataSimpleKnownVector(t *testing.T) {
	// Minimal Simple profile header: fastuvmc and res_fasttx set, res_rtm
	// set, everything else zero.
	got, err := ParseExtradata([]byte{0x00, 0x01, 0x80, 0x01})
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Profile:    ProfileSimple,
		FastUVMC:   true,
		ResFastTX:  true,
		ResRTMFlag: true,
	}
	if got != want {
		t.Fatalf("parsed config:\n got %#v\nwant %#v", got, want)
	}
}

func TestParseExtradataSpriteVector(t *testing.T) {
	// Main profile sprite (WMV3IMAGE) header: 320x240 sprite, variable
	// transform, no optional sprite features.
	data := []byte{0x50, 0x01, 0x88, 0x00, 0x50, 0x07, 0x80, 0x00}
	got, err := ParseExtradata(data)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Profile:       ProfileMain,
		FastUVMC:      true,
		VSTransform:   true,
		ResFastTX:     true,
		Sprite:        true,
		SpriteWidth:   320,
		SpriteHeight:  240,
		QuantizerMode: QuantFrameImplicit,
	}
	if got != want {
		t.Fatalf("parsed config:\n got %#v\nwant %#v", got, want)
	}
}

func TestParseExtradataReservedFastTX(t *testing.T) {
	// res_fasttx=0 is followed by the reserved 16-bit field.
	data := []byte{0x40, 0x00, 0x80, 0x00, 0x40, 0x2f}
	got, err := ParseExtradata(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResFastTX || !got.FastUVMC || got.Profile != ProfileMain {
		t.Fatalf("parsed config: %#v", got)
	}
	if _, err := ParseExtradata(data[:5]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("missing reserved field: want ErrTruncated, got %v", err)
	}
}

func TestParseExtradataSimpleLoopFilterAccepted(t *testing.T) {
	// FFmpeg logs an error for loop_filter=1 in Simple Profile but keeps
	// parsing and retains the flag; the Go parser matches that behavior.
	h := simpleHeader()
	h.loopFilter = true
	got, err := ParseExtradata(h.bytes())
	if err != nil {
		t.Fatalf("simple loop filter: %v", err)
	}
	if !got.LoopFilter || got.Profile != ProfileSimple {
		t.Fatalf("parsed config: %#v", got)
	}
}

func TestParseExtradataDeterministic(t *testing.T) {
	h := validHeader()
	h.frmrtq = 5
	h.bitrtq = 17
	h.multiRes = true
	h.extendedMV = true
	h.dquant = 2
	h.vsTransform = true
	h.overlap = true
	h.resync = true
	h.rangered = true
	h.maxBFrames = 2
	h.quantizer = 1
	h.finterp = true
	data := h.bytes()
	first, err := ParseExtradata(data)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Profile:        ProfileMain,
		FrmRTQPostProc: 5,
		BitRTQPostProc: 17,
		MultiRes:       true,
		FastUVMC:       true,
		ExtendedMV:     true,
		DQuant:         2,
		VSTransform:    true,
		Overlap:        true,
		ResyncMarker:   true,
		Rangered:       true,
		MaxBFrames:     2,
		QuantizerMode:  QuantFrameExplicit,
		FInterpFlag:    true,
		ResFastTX:      true,
		ResRTMFlag:     true,
	}
	if first != want {
		t.Fatalf("parsed config:\n got %#v\nwant %#v", first, want)
	}
	second, err := ParseExtradata(append([]byte(nil), data...))
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("nondeterministic parse:\nfirst %#v\nsecond %#v", first, second)
	}
}

func TestParseExtradataRejects(t *testing.T) {
	spriteBadSize := validHeader()
	spriteBadSize.sprite = true
	spriteBadSize.spriteW = 320

	spriteBadHeight := validHeader()
	spriteBadHeight.sprite = true
	spriteBadHeight.spriteH = 240

	spriteDC := validHeader()
	spriteDC.sprite = true
	spriteDC.spriteW = 320
	spriteDC.spriteH = 240
	spriteDC.spriteDC = true

	transtab := validHeader()
	transtab.transtab = true

	oldInterlace := validHeader()
	oldInterlace.oldInterlace = true

	simpleNoFastUVMC := simpleHeader()
	simpleNoFastUVMC.fastUVMC = false

	simpleExtMV := simpleHeader()
	simpleExtMV.extendedMV = true

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"complex profile", profileBits(2), ErrUnsupportedProfile},
		{"advanced profile", profileBits(3), ErrUnsupportedProfile},
		{"old interlaced mode", oldInterlace.bytes(), ErrInvalidValue},
		{"reserved transtab", transtab.bytes(), ErrInvalidValue},
		{"simple without fastuvmc", simpleNoFastUVMC.bytes(), ErrInvalidValue},
		{"simple with extended mv", simpleExtMV.bytes(), ErrInvalidValue},
		{"sprite zero height", spriteBadSize.bytes(), ErrInvalidValue},
		{"sprite zero width", spriteBadHeight.bytes(), ErrInvalidValue},
		{"sprite dc vlc feature", spriteDC.bytes(), ErrUnsupportedFeature},
	}
	for _, tc := range cases {
		if _, err := ParseExtradata(tc.data); !errors.Is(err, tc.want) {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}
}

func TestParseExtradataTruncated(t *testing.T) {
	full := []byte{0x4b, 0x1b, 0xeb, 0xaf} // 32-bit Main header
	for n := range len(full) {
		if _, err := ParseExtradata(full[:n]); !errors.Is(err, ErrTruncated) {
			t.Errorf("prefix %d: want ErrTruncated, got %v", n, err)
		}
	}
	sprite := []byte{0x50, 0x01, 0x88, 0x00, 0x50, 0x07, 0x80, 0x00} // 63-bit sprite header
	for n := range len(sprite) {
		if _, err := ParseExtradata(sprite[:n]); !errors.Is(err, ErrTruncated) {
			t.Errorf("sprite prefix %d: want ErrTruncated, got %v", n, err)
		}
	}
}

func TestProfileString(t *testing.T) {
	if ProfileSimple.String() != "simple" || ProfileMain.String() != "main" ||
		ProfileComplex.String() != "complex" || ProfileAdvanced.String() != "advanced" {
		t.Fatal("unexpected profile names")
	}
	if Profile(7).String() != "unknown(7)" {
		t.Fatalf("unknown profile: %q", Profile(7).String())
	}
}
