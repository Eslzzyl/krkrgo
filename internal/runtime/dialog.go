package runtime

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrDialogUnavailable = errors.New("dialog handler unavailable")
var ErrDialogCancelled = errors.New("dialog cancelled")

// DialogRequest is a host-neutral message dialog request. Buttons are labels
// in display order; an empty list means the host's default acknowledgement.
type DialogRequest struct {
	Title   string   `json:"title,omitempty"`
	Message string   `json:"message"`
	Buttons []string `json:"buttons,omitempty"`
	Default int      `json:"default,omitempty"`
	Cancel  int      `json:"cancel,omitempty"`
}

type DialogResult struct {
	Button      string `json:"button,omitempty"`
	ButtonIndex int    `json:"button_index"`
	Accepted    bool   `json:"accepted"`
	Text        string `json:"text,omitempty"`
}

type DialogHandler func(DialogRequest) (DialogResult, error)

// DialogService is safe to share across a session. The handler is captured
// before invocation so a host can replace it without blocking a dialog.
type DialogService struct {
	mu      sync.RWMutex
	Handler DialogHandler
}

func NewDialogService(handler DialogHandler) *DialogService { return &DialogService{Handler: handler} }
func (d *DialogService) SetHandler(handler DialogHandler) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.Handler = handler
	d.mu.Unlock()
}
func (d *DialogService) handler() DialogHandler {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	h := d.Handler
	d.mu.RUnlock()
	return h
}
func (d *DialogService) Show(request DialogRequest) (DialogResult, error) {
	h := d.handler()
	if h == nil {
		return DialogResult{}, ErrDialogUnavailable
	}
	if request.Default < -1 || request.Default >= len(request.Buttons) || request.Cancel < -1 || request.Cancel >= len(request.Buttons) {
		return DialogResult{}, fmt.Errorf("invalid dialog button index")
	}
	result, err := h(request)
	if err != nil {
		return DialogResult{}, err
	}
	if result.ButtonIndex < -1 || result.ButtonIndex >= len(request.Buttons) {
		return DialogResult{}, fmt.Errorf("invalid dialog result button index")
	}
	if result.Button == "" && result.ButtonIndex >= 0 {
		result.Button = request.Buttons[result.ButtonIndex]
	}
	if result.ButtonIndex < 0 && !result.Accepted {
		return result, ErrDialogCancelled
	}
	return result, nil
}
func (d *DialogService) ShowMessage(title, message string) (DialogResult, error) {
	return d.Show(DialogRequest{Title: title, Message: message, Buttons: []string{"OK"}, Default: 0, Cancel: -1})
}
func (d *DialogService) Confirm(title, message string) (bool, error) {
	r, err := d.Show(DialogRequest{Title: title, Message: message, Buttons: []string{"OK", "Cancel"}, Default: 0, Cancel: 1})
	if err != nil {
		return false, err
	}
	return r.Accepted && r.ButtonIndex != 1 && !strings.EqualFold(r.Button, "cancel"), nil
}
func (d *DialogService) Handle(request DialogRequest) (DialogResult, error) { return d.Show(request) }
