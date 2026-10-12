// Tables translated from FFmpeg libavcodec/vc1data.c, libavcodec/vc1data.h
// and libavcodec/vc1_vlc_data.h (LGPL-2.1-or-later):
//
//	Copyright (c) 2011 Mashiat Sarker Shakkhar
//	Copyright (c) 2006-2007 Konstantin Shishkov
//	Partly based on vc9.c (c) 2005 Anonymous, Alex Beregszaszi, Michael Niedermayer
//
// FFmpeg is free software; you can redistribute it and/or modify it under
// the terms of the GNU Lesser General Public License as published by the
// Free Software Foundation; either version 2.1 of the License, or (at your
// option) any later version. See doc.go for the full license notice.
//
// Only the tables needed by the progressive Simple/Main I/P frame-header
// slice are transcribed here: quantizer maps, P-mode maps, TT_FRM map,
// B-fraction scales, and the bitplane IMODE/NORM2/NORM6 VLC codebooks.
// Larger coefficient/MV tables remain future work and are reported by the
// decoder instead of being guessed.
package wmv3

// PQuantTable maps the 5-bit PQINDEX to the frame quantizer PQ, per
// ff_vc1_pquant_table. Row 0 is the implicit quantizer; row 1 is the
// explicit uniform quantizer used by ff_vc1_parse_frame_header for any
// non-implicit QUANTIZER mode. Row 2 (explicit non-uniform) is carried
// for the macroblock layer, which is not decoded yet.
var PQuantTable = [3][32]uint8{
	{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 6, 7, 8, 9, 10, 11, 12,
		13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 27, 29, 31,
	},
	{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
	},
	{
		0, 1, 1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13,
		14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 29, 31,
	},
}

// MV P-mode values, matching enum MVModes in libavcodec/vc1.h.
const (
	MVMode1MVHPelBilin  uint8 = 0
	MVMode1MV           uint8 = 1
	MVMode1MVHPel       uint8 = 2
	MVModeMixed         uint8 = 3
	MVModeIntensityComp uint8 = 4
)

// MVPModeTable maps get_unary(gb,1,4) to an MV P-mode, per
// ff_vc1_mv_pmode_table. Row 0 selects the low-rate table when PQ>12.
var MVPModeTable = [2][5]uint8{
	{MVMode1MVHPelBilin, MVMode1MV, MVMode1MVHPel, MVModeIntensityComp, MVModeMixed},
	{MVMode1MV, MVModeMixed, MVMode1MVHPel, MVModeIntensityComp, MVMode1MVHPelBilin},
}

// MVPModeTable2 maps the second unary code to an MV P-mode when intensity
// compensation is selected, per ff_vc1_mv_pmode_table2.
var MVPModeTable2 = [2][4]uint8{
	{MVMode1MVHPelBilin, MVMode1MV, MVMode1MVHPel, MVModeMixed},
	{MVMode1MV, MVModeMixed, MVMode1MVHPel, MVMode1MVHPelBilin},
}

// Transform types, matching enum TransformTypes in libavcodec/vc1.h.
const (
	TT8x8       uint8 = 0
	TT8x4Bottom uint8 = 1
	TT8x4Top    uint8 = 2
	TT8x4       uint8 = 3
	TT4x8Right  uint8 = 4
	TT4x8Left   uint8 = 5
	TT4x8       uint8 = 6
	TT4x4       uint8 = 7
)

// BFractionLUT holds the pre-computed B-fraction scales for base 256, per
// ff_vc1_bfraction_lut with B_FRACTION_DEN=256 (libavcodec/vc1data.h:100).
// Index 21 is invalid; index 22 selects BI frames. The header slice only
// uses this table to consume (then reject) B pictures deterministically.
var BFractionLUT = [23]int16{
	128, 85, 170, 64,
	192, 51, 102,
	153, 204, 43, 215,
	37, 74, 111, 148,
	185, 222, 32, 96,
	160, 224,
	-1, 0,
}

// ttfrmToTT maps the 2-bit TTFRM syntax element to a TransformTypes
// value, per ff_vc1_ttfrm_to_tt in libavcodec/vc1data.c.
var ttfrmToTT = [4]uint8{TT8x8, TT8x4, TT4x8, TT4x4}

// Bitplane IMODE values, matching enum Imode in libavcodec/vc1.h.
const (
	ImodeRaw     = 0
	ImodeNorm2   = 1
	ImodeDiff2   = 2
	ImodeNorm6   = 3
	ImodeDiff6   = 4
	ImodeRowSkip = 5
	ImodeColSkip = 6
)

// imodeCodes/imodeBits are the 7-entry IMODE codebook from
// libavcodec/vc1_vlc_data.h (vc1_imode_codes/vc1_imode_bits).
var imodeCodes = []uint16{0, 2, 1, 3, 1, 2, 3}
var imodeBits = []uint8{4, 2, 3, 2, 4, 3, 3}

// norm2Codes/norm2Bits are the NORM-2 codebook (vc1_norm2_codes/bits).
var norm2Codes = []uint16{0, 4, 5, 3}
var norm2Bits = []uint8{1, 3, 3, 2}

// norm6Codes/norm6Bits are the 64-entry NORM-6 codebook
// (vc1_norm6_codes/bits).
var norm6Codes = []uint16{
	0x001, 0x002, 0x003, 0x000, 0x004, 0x001, 0x002, 0x047, 0x005, 0x003, 0x004, 0x04B, 0x005, 0x04D, 0x04E, 0x30E,
	0x006, 0x006, 0x007, 0x053, 0x008, 0x055, 0x056, 0x30D, 0x009, 0x059, 0x05A, 0x30C, 0x05C, 0x30B, 0x30A, 0x037,
	0x007, 0x00A, 0x00B, 0x043, 0x00C, 0x045, 0x046, 0x309, 0x00D, 0x049, 0x04A, 0x308, 0x04C, 0x307, 0x306, 0x036,
	0x00E, 0x051, 0x052, 0x305, 0x054, 0x304, 0x303, 0x035, 0x058, 0x302, 0x301, 0x034, 0x300, 0x033, 0x032, 0x007,
}
var norm6Bits = []uint8{
	1, 4, 4, 8, 4, 8, 8, 10, 4, 8, 8, 10, 8, 10, 10, 13,
	4, 8, 8, 10, 8, 10, 10, 13, 8, 10, 10, 13, 10, 13, 13, 9,
	4, 8, 8, 10, 8, 10, 10, 13, 8, 10, 10, 13, 10, 13, 13, 9,
	8, 10, 10, 13, 10, 13, 13, 9, 10, 13, 13, 9, 13, 9, 9, 6,
}
