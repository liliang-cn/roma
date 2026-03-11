//go:build windows

package runtime

import (
	"fmt"
	"os/exec"
)

type platformPTYProvider struct{}

func newPTYProvider() PTYProvider {
	return platformPTYProvider{}
}

func (platformPTYProvider) Start(cmd *exec.Cmd) (PTYProcess, error) {
	return nil, fmt.Errorf("pty provider is not implemented on windows yet")
}
