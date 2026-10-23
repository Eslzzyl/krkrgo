package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// BuildArgs returns the command line that starts g with the runtime.
func BuildArgs(g Game) ([]string, error) {
	if err := ValidateGame(g); err != nil {
		return nil, err
	}
	args := []string{"run", "-project", g.Project}
	if g.SaveDir != "" {
		args = append(args, "-save-dir", g.SaveDir)
	}
	if g.Budget != 0 {
		args = append(args, "-budget", strconv.FormatUint(g.Budget, 10))
	}
	if g.Renderer != "" {
		args = append(args, "-renderer", g.Renderer)
	}
	return args, nil
}

// Process is one running game.
type Process struct {
	Cmd     *exec.Cmd
	LogPath string
	Started time.Time

	logFile *os.File
	done    chan struct{}
	stop    sync.Once
}

// Start launches game as a child process of the launcher, writing its
// standard output and error to logPath.
func Start(game Game, executable, logPath string) (*Process, error) {
	args, err := BuildArgs(game)
	if err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open game log: %w", err)
	}
	cmd := exec.Command(executable, args...)
	cmd.Dir = projectDirectory(game.Project)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start game: %w", err)
	}
	return &Process{
		Cmd:     cmd,
		LogPath: logPath,
		Started: time.Now(),
		logFile: logFile,
		done:    make(chan struct{}),
	}, nil
}

// Stop asks the game to exit, then kills it when it has not exited after a
// short grace period. It is safe to call more than once.
func (p *Process) Stop() {
	p.stop.Do(func() {
		if p.Cmd.Process == nil {
			return
		}
		pid := p.Cmd.Process.Pid
		terminateProcess(p.Cmd)
		time.AfterFunc(3*time.Second, func() {
			select {
			case <-p.done:
			default:
				_ = killProcess(pid)
			}
		})
	})
}

// Wait waits for the process to exit and closes its log.
func (p *Process) Wait() error {
	err := p.Cmd.Wait()
	close(p.done)
	if p.logFile != nil {
		_ = p.logFile.Close()
	}
	return err
}
