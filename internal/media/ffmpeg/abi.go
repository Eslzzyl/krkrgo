package ffmpeg

import (
	"syscall"
	"unsafe"
)

type avRational struct {
	Num int32
	Den int32
}

type avChannelLayoutABI struct {
	Order      int32
	Channels   int32
	MaskOrMap  uintptr
	Opaque     uintptr
}

type avFormatContextABI struct {
	AVClass       uintptr
	IFormat       uintptr
	OFormat       uintptr
	PrivData      uintptr
	PB            uintptr
	ContextFlags  int32
	StreamCount   uint32
	Streams       uintptr
	GroupCount    uint32
	_groupPad     uint32
	StreamGroups  uintptr
	ChapterCount  uint32
	_chapterPad   uint32
	Chapters      uintptr
	URL           uintptr
	StartTime     int64
	Duration      int64
	BitRate       int64
	PacketSize    uint32
	MaxDelay      int32
	Flags         int32
}

type avStreamABI struct {
	AVClass          uintptr
	Index            int32
	ID               int32
	CodecPar         uintptr
	PrivData         uintptr
	TimeBase         avRational
	StartTime        int64
	Duration         int64
	FrameCount       int64
	Disposition      int32
	Discard          int32
	SampleAspect     avRational
	Metadata         uintptr
	AverageFrameRate avRational
	RealFrameRate    avRational
}

type avCodecParametersABI struct {
	CodecType          int32
	CodecID            int32
	CodecTag           uint32
	Extradata          uintptr
	ExtradataSize      int32
	_pad0              int32
	CodedSideData      uintptr
	CodedSideDataCount int32
	Format             int32
	BitRate            int64
	BitsPerCodedSample int32
	BitsPerRawSample   int32
	Profile            int32
	Level               int32
	Width               int32
	Height              int32
	SampleAspect        avRational
	FrameRate           avRational
	FieldOrder          int32
	ColorRange          int32
	ColorPrimaries      int32
	ColorTransfer       int32
	ColorSpace          int32
	ChromaLocation      int32
	VideoDelay          int32
	_layoutPad          int32
	ChannelLayout       avChannelLayoutABI
	SampleRate          int32
	BlockAlign          int32
	FrameSize           int32
	InitialPadding      int32
	TrailingPadding     int32
	SeekPreroll         int32
}

type avPacketABI struct {
	Buf           uintptr
	PTS           int64
	DTS           int64
	Data          uintptr
	Size          int32
	StreamIndex   int32
	Flags         int32
	SideData      uintptr
	SideDataCount int32
	Duration      int64
	Position      int64
	Opaque        uintptr
	OpaqueRef     uintptr
	TimeBase      avRational
}

type avFrameABI struct {
	Data          [8]uintptr
	LineSize      [8]int32
	ExtendedData  uintptr
	Width         int32
	Height        int32
	SampleCount   int32
	Format        int32
	PictureType   int32
	SampleAspect  avRational
	_ptsPad       uint32
	PTS           int64
	PacketDTS     int64
	TimeBase      avRational
	Quality       int32
	Opaque        uintptr
	RepeatPicture int32
	SampleRate    int32
	Buffers       [8]uintptr
}

const (
	mediaTypeVideo = 0
	mediaTypeAudio = 1

	pixelFormatRGBA = 26

	sampleFormatU8   = 0
	sampleFormatS16  = 1
	sampleFormatS32  = 2
	sampleFormatFLT  = 3
	sampleFormatDBL  = 4
	sampleFormatU8P  = 5
	sampleFormatS16P = 6
	sampleFormatS32P = 7
	sampleFormatFLTP = 8
	sampleFormatDBLP = 9

	avSeekSize       = 0x10000
	avSeekBackward   = 1
	avFormatCustomIO = 0x0080
	avNoPTSValue     = int64(-1 << 63)
	avErrorAgain     = -int32(syscall.EAGAIN)
	avErrorEOF       = int32(-541478725)
)

func cString(s string) []byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}

func ptrAt(base uintptr, index uintptr) uintptr {
	return *(*uintptr)(unsafe.Pointer(base + index*unsafe.Sizeof(uintptr(0))))
}

func readBytes(address uintptr, size int) []byte {
	if address == 0 || size <= 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(address)), size)
}
