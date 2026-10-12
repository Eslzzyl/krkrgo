// Package asf implements a bounded demuxer for ASF (Advanced Systems
// Format) containers, the family that carries WMV/WMA files such as the
// lovelyday logo.wmv movie member.
//
// The parser is byte-slice based and read-only: Parse extracts the file
// header, file properties, and stream descriptors; Packets iterates data
// packets with bounded payload parsing; NewDemuxer reassembles fragmented
// media objects into access units with preroll-adjusted timestamps and
// keyframe flags.
//
// DRM-protected content (Content Encryption, Extended Content Encryption)
// is rejected with ErrEncrypted rather than partially parsed. Broadcast
// (streaming) files, which have no fixed packet size or data-object
// length, are rejected with ErrUnsupported. Packet descrambling and the
// compressed multi-packet audio mode are likewise explicit unsupported
// cases rather than silent corruption.
//
// The demuxer targets ordinary local ASF files; seeking, index objects,
// metadata extraction, and codec decoding are out of scope.
package asf
