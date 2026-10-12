// Package wmv3 parses WMV3 (Windows Media Video 9, Simple/Main profile)
// bitstreams: an MSB-first bit reader, the Simple/Main sequence header
// carried in the codec extradata, and the progressive Simple/Main frame
// header needed to reach the macroblock layer.
//
// The bit layout and validation rules are translated from FFmpeg
// (libavcodec/vc1.c ff_vc1_decode_sequence_header,
// ff_vc1_decode_init, ff_vc1_parse_frame_header and libavcodec/vc1dec.c
// vc1_decode_init):
//
//	Copyright (c) 2011 Mashiat Sarker Shakkhar
//	Copyright (c) 2006-2007 Konstantin Shishkov
//	Partly based on vc9.c (c) 2005 Anonymous, Alex Beregszaszi, Michael Niedermayer
//
// FFmpeg is free software; you can redistribute it and/or modify it under
// the terms of the GNU Lesser General Public License as published by the
// Free Software Foundation; either version 2.1 of the License, or (at your
// option) any later version.
//
// Only Simple and Main profiles are parsed; Complex and Advanced profile
// extradata is rejected with an error wrapping ErrUnsupportedProfile.
// Container demuxing (ASF), frame unescaping, and the picture (macroblock)
// layer are out of scope for this package.
//
// Supported so far: sequence-header parsing from extradata
// (ParseExtradata), progressive Simple/Main frame-header parsing for I,
// P, B, and BI pictures (Decoder.ParseFrameHeader), and the macroblock
// bitplanes the header owns. Picture decoding is not implemented:
// Decoder.Decode reports ErrPictureUnavailable for I and P pictures and
// ErrUnsupportedFeature for B and BI pictures, X8-transform frames, and
// sprite streams. The next slice is the macroblock layer
// (libavcodec/vc1_block.c and its coefficient/transform tables).
package wmv3
