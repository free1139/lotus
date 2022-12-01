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
	names := strings.Split(string(content), "\n")
	result := make([]string, len(names))
	i := 0
	for _, name := range names {
		if len(name) == 0 {
			continue
		}
		result[i] = name
		i++
	}
	return result[:i], nil
}
