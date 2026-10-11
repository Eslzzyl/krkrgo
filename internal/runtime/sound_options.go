package runtime

import "fmt"

// SoundOptions stores the nine-byte ktsndopt dialog state: for each of Music,
// Sound effects, and Voice, volume, reduce-during-voice, and voice volume.
type SoundOptions struct {
	state       [9]uint8
	initialized bool
}

// NewSoundOptions returns an uninitialized options dialog state.
func NewSoundOptions() *SoundOptions { return &SoundOptions{} }

// Init consumes the same fifteen integer/bool slots as ktSndOptDlg_Init:
// visible, volume, voice-options-visible, reduce checkbox, silence volume for
// each of three groups. Values are validated and copied into GetState format.
func (o *SoundOptions) Init(args ...int) error {
	if o == nil {
		return fmt.Errorf("nil sound options")
	}
	if len(args) != 15 {
		return fmt.Errorf("sound options init requires 15 arguments")
	}
	var state [9]uint8
	for group := range 3 {
		base := group * 5
		volume, silence := args[base+1], args[base+4]
		if volume < 0 || volume > 100 || silence < 0 || silence > 100 {
			return fmt.Errorf("sound option volumes must be 0..100")
		}
		state[group*3] = uint8(volume)
		if args[base+3] != 0 {
			state[group*3+1] = 1
		}
		state[group*3+2] = uint8(silence)
	}
	o.state, o.initialized = state, true
	return nil
}

// GetState returns a copy of the nine-byte dialog state.
func (o *SoundOptions) GetState() ([]byte, error) {
	if o == nil || !o.initialized {
		return nil, fmt.Errorf("sound options dialog is not initialized")
	}
	return append([]byte(nil), o.state[:]...), nil
}

// SetState replaces state after validating all nine fields.
func (o *SoundOptions) SetState(state []byte) error {
	if o == nil {
		return fmt.Errorf("nil sound options")
	}
	if len(state) != 9 {
		return fmt.Errorf("sound options state requires 9 values")
	}
	for i, value := range state {
		if value > 100 || (i%3 == 1 && value > 1) {
			return fmt.Errorf("sound option value is out of range")
		}
	}
	copy(o.state[:], state)
	o.initialized = true
	return nil
}

// Final closes the dialog state.
func (o *SoundOptions) Final() {
	if o != nil {
		o.initialized = false
		o.state = [9]uint8{}
	}
}

// SoundOptionsCall is a headless translation of the plugin entry points. Init
// returns true on success; GetState returns a copy; Final returns no state.
func SoundOptionsCall(options *SoundOptions, name string, args ...int) ([]byte, bool, error) {
	if options == nil {
		options = NewSoundOptions()
	}
	switch name {
	case "ktSndOptDlg_Init":
		if err := options.Init(args...); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	case "ktSndOptDlg_GetState":
		state, err := options.GetState()
		return state, false, err
	case "ktSndOptDlg_Final":
		options.Final()
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("unsupported sound options operation %s", name)
	}
}
