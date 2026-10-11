package runtime

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrShellUnavailable = errors.New("shell execute handler unavailable")
var ErrShellRejected = errors.New("shell execute rejected")

type ShellExecuteRequest struct {
	Target     string `json:"target"`
	Verb       string `json:"verb,omitempty"`
	Arguments  string `json:"arguments,omitempty"`
	WorkingDir string `json:"working_dir,omitempty"`
}
type ShellExecuteResult struct {
	Started bool  `json:"started"`
	PID     int64 `json:"pid,omitempty"`
	Code    int   `json:"code,omitempty"`
}
type ShellExecuteHandler func(ShellExecuteRequest) (ShellExecuteResult, error)

type ShellExecuteService struct {
	mu      sync.RWMutex
	Handler ShellExecuteHandler
}
type ShellService = ShellExecuteService

func NewShellExecuteService(handler ShellExecuteHandler) *ShellExecuteService {
	return &ShellExecuteService{Handler: handler}
}
func NewShellService(handler ShellExecuteHandler) *ShellExecuteService {
	return NewShellExecuteService(handler)
}
func (s *ShellExecuteService) SetHandler(handler ShellExecuteHandler) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.Handler = handler
	s.mu.Unlock()
}
func (s *ShellExecuteService) handler() ShellExecuteHandler {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	h := s.Handler
	s.mu.RUnlock()
	return h
}
func (s *ShellExecuteService) Execute(request ShellExecuteRequest) (ShellExecuteResult, error) {
	if strings.TrimSpace(request.Target) == "" || strings.IndexByte(request.Target, 0) >= 0 || strings.IndexByte(request.WorkingDir, 0) >= 0 {
		return ShellExecuteResult{}, fmt.Errorf("%w: invalid target", ErrShellRejected)
	}
	h := s.handler()
	if h == nil {
		return ShellExecuteResult{}, ErrShellUnavailable
	}
	result, err := h(request)
	if err != nil {
		return ShellExecuteResult{}, err
	}
	if !result.Started {
		return result, ErrShellRejected
	}
	return result, nil
}
func (s *ShellExecuteService) Open(target string) (ShellExecuteResult, error) {
	return s.Execute(ShellExecuteRequest{Target: target, Verb: "open"})
}
func (s *ShellExecuteService) Handle(request ShellExecuteRequest) (ShellExecuteResult, error) {
	return s.Execute(request)
}
