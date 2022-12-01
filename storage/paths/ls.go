package paths

import (
	"os/exec"
	"strings"
)

func ReadDir(path string) ([]string, error) {
	content, err := exec.Command("ls", path).CombinedOutput()
	if err != nil {
		return nil, err
	}
	return strings.Split(string(content), "\n"), nil
}
