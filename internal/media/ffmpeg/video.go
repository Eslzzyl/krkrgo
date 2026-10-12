package ffmpeg

import (
	"fmt"
	"runtime"
	"unsafe"
)

func (d *Decoder) convertVideo(state *streamState) (*Frame, error) {
	avFrame := (*avFrameABI)(unsafe.Pointer(state.frame))
	if avFrame.Width <= 0 || avFrame.Height <= 0 || avFrame.Width > 1<<15 || avFrame.Height > 1<<15 {
		return nil, fmt.Errorf("%w: invalid video dimensions %dx%d", ErrMalformed, avFrame.Width, avFrame.Height)
	}
	pixels := uint64(avFrame.Width) * uint64(avFrame.Height)
	if pixels > MaxFrameBytes/4 {
		return nil, fmt.Errorf("%w: video frame exceeds %d bytes", ErrMalformed, MaxFrameBytes)
	}
	if avFrame.Data[0] == 0 || avFrame.LineSize[0] == 0 {
		return nil, fmt.Errorf("%w: decoded video frame has no pixels", ErrMalformed)
	}
	if d.sws == 0 {
		d.sws = d.api.swsGetContext(avFrame.Width, avFrame.Height, avFrame.Format, avFrame.Width, avFrame.Height, pixelFormatRGBA, 2, 0, 0, 0)
		if d.sws == 0 {
			return nil, fmt.Errorf("%w: allocate RGBA conversion context", ErrUnavailable)
		}
	}
	rgba := make([]byte, int(pixels)*4)
	srcData := avFrame.Data
	srcLines := avFrame.LineSize
	dstData := [8]uintptr{uintptr(unsafe.Pointer(&rgba[0]))}
	dstLines := [8]int32{avFrame.Width * 4}
	converted := d.api.swsScale(d.sws, uintptr(unsafe.Pointer(&srcData[0])), uintptr(unsafe.Pointer(&srcLines[0])), 0, avFrame.Height, uintptr(unsafe.Pointer(&dstData[0])), uintptr(unsafe.Pointer(&dstLines[0])))
	runtime.KeepAlive(rgba)
	if converted != avFrame.Height {
		return nil, fmt.Errorf("%w: sws_scale converted %d of %d rows", ErrMalformed, converted, avFrame.Height)
	}
	pts := avFrame.PTS
	if pts == avNoPTSValue {
		pts = avFrame.PacketDTS
	}
	timeBase := avFrame.TimeBase
	if timeBase.Num <= 0 || timeBase.Den <= 0 {
		timeBase = state.timeBase
	}
	frame := &Frame{Index: d.frameSeq, Width: avFrame.Width, Height: avFrame.Height, PTSMS: rescaleMS(pts, timeBase), RGBA: rgba}
	d.frameSeq++
	runtime.KeepAlive(d)
	return frame, nil
}
