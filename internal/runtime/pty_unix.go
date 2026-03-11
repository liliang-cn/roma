//go:build !windows

package runtime

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type platformPTYProvider struct{}

func newPTYProvider() PTYProvider {
	return platformPTYProvider{}
}

func (platformPTYProvider) Start(cmd *exec.Cmd) (PTYProcess, error) {
	file, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &ptyProcess{
		file: file,
		cmd:  cmd,
	}, nil
}

type ptyProcess struct {
	file *os.File
	cmd  *exec.Cmd
}

func (p *ptyProcess) Read(data []byte) (int, error) {
	return p.file.Read(data)
}

func (p *ptyProcess) Write(data []byte) (int, error) {
	return p.file.Write(data)
}

func (p *ptyProcess) Close() error {
	return p.file.Close()
}

func (p *ptyProcess) Wait() error {
	return p.cmd.Wait()
}
