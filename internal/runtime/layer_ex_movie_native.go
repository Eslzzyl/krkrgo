package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func (s *Services) clearLayerMovie(id tjs.ObjectID) {
	if s == nil {
		return
	}
	s.ensureInit()
	movie := s.LayerMovies[id]
	if movie == nil {
		return
	}
	delete(s.LayerMovies, id)
	if s.AudioMixer != nil {
		s.AudioMixer.RemoveVideo(movie)
	}
	movie.Close()
}

func (s *Services) layerExMovieNative(id tjs.ObjectID, layer *Layer, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	movie := s.LayerMovies[id]
	switch operation {
	case "openMovie":
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Layer.openMovie: missing argument 0")
		}
		name := args[0].Text()
		alpha := false
		if len(args) > 1 && !args[1].IsVoid() {
			var err error
			alpha, err = args[1].Truth()
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		s.clearLayerMovie(id)
		storageName, data, err := s.readVideoStorage(name)
		if err != nil {
			return tjs.VoidValue, videoErr(name, err)
		}
		movie = NewVideoOverlay()
		if err := movie.Open(storageName, data); err != nil {
			return tjs.VoidValue, err
		}
		if err := movie.SetLayerWithAlpha(0, layer, alpha); err != nil {
			movie.Close()
			return tjs.VoidValue, err
		}
		layerType := LayerOpaque
		if alpha {
			layerType = LayerAlpha
		}
		if err := layer.SetType(layerType); err != nil {
			movie.Close()
			return tjs.VoidValue, err
		}
		if _, ok := movie.Source(); ok {
			if err := layer.SetSize(layer.ImageWidth, layer.ImageHeight); err != nil {
				movie.Close()
				return tjs.VoidValue, err
			}
		}
		movie.SetClock(s.TimeMS)
		s.LayerMovies[id] = movie
		if s.AudioMixer != nil {
			s.AudioMixer.AddVideo(movie)
		}
		return tjs.VoidValue, nil
	case "startMovie":
		if movie == nil {
			return tjs.VoidValue, nil
		}
		loop := false
		if len(args) > 0 && !args[0].IsVoid() {
			var err error
			loop, err = args[0].Truth()
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		movie.SetLoop(loop)
		return tjs.VoidValue, movie.Play()
	case "stopMovie":
		if movie == nil {
			return tjs.VoidValue, nil
		}
		err := movie.Stop()
		s.clearLayerMovie(id)
		return tjs.VoidValue, err
	case "isPlayingMovie":
		return tjs.IntegerValue(boolInt(movie != nil && movie.Status() == VideoPlaying)), nil
	default:
		return tjs.VoidValue, fmt.Errorf("unknown Layer movie operation: %s", operation)
	}
}
